package sextantd

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestMapLifecycleTransitionExhaustive locks the transition→state mapping
// the watcher applies. Every LifecycleEvent the proto defines must have
// an entry here; an unmapped transition is a no-op (returns ok=false).
func TestMapLifecycleTransitionExhaustive(t *testing.T) {
	cases := []struct {
		in      sextantproto.LifecycleEvent
		want    sextantproto.LifecycleState
		applies bool
	}{
		{sextantproto.LifecycleStarted, sextantproto.LifecycleRunning, true},
		{sextantproto.LifecycleEnded, sextantproto.LifecycleEndedState, true},
		{sextantproto.LifecyclePausedEvent, sextantproto.LifecyclePaused, true},
		{sextantproto.LifecycleResumedEvent, sextantproto.LifecycleRunning, true},
		{sextantproto.LifecycleArchivedEvent, sextantproto.LifecycleArchived, true},
		{sextantproto.LifecycleRestartedEvent, sextantproto.LifecycleRunning, true},
		{sextantproto.LifecycleCrashedEvent, sextantproto.LifecycleCrashedState, true},
		{sextantproto.LifecycleTurnEnded, "", false},
		{sextantproto.LifecycleEvent("future_event"), "", false},
	}
	for _, tc := range cases {
		got, ok := MapLifecycleTransition(tc.in)
		if ok != tc.applies {
			t.Errorf("MapLifecycleTransition(%q) applies = %v, want %v", tc.in, ok, tc.applies)
		}
		if got != tc.want {
			t.Errorf("MapLifecycleTransition(%q) state = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLifecycleWatcherAppliesTransition covers the four state-changing
// transitions the ticket calls out: ended, crashed, paused, archived.
// Drives the watcher's apply path directly against a fake KV so the
// test is deterministic and doesn't depend on a live nats-server.
func TestLifecycleWatcherAppliesTransition(t *testing.T) {
	cases := []struct {
		name       string
		transition sextantproto.LifecycleEvent
		want       sextantproto.LifecycleState
	}{
		{"ended", sextantproto.LifecycleEnded, sextantproto.LifecycleEndedState},
		{"crashed", sextantproto.LifecycleCrashedEvent, sextantproto.LifecycleCrashedState},
		{"paused", sextantproto.LifecyclePausedEvent, sextantproto.LifecyclePaused},
		{"archived", sextantproto.LifecycleArchivedEvent, sextantproto.LifecycleArchived},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kv := newFakeLifecycleKV()
			id := uuid.New()
			kv.seedDefinition(t, id, "alpha", sextantproto.LifecycleRunning, 1)

			w := &LifecycleWatcher{defs: kv}
			w.handle(envelopeFor(t, id, tc.transition))

			got := kv.currentLifecycle(t, id)
			if got != tc.want {
				t.Errorf("Lifecycle = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLifecycleWatcherIgnoresTurnEnded — turn_ended is a per-turn signal,
// not an agent-lifecycle transition. The watcher must leave the record
// alone (no Put, no version bump).
func TestLifecycleWatcherIgnoresTurnEnded(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	kv.seedDefinition(t, id, "beta", sextantproto.LifecycleRunning, 1)

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleTurnEnded))

	if got := kv.currentLifecycle(t, id); got != sextantproto.LifecycleRunning {
		t.Errorf("Lifecycle = %q, want unchanged (running)", got)
	}
	if kv.putCount() != 1 { // 1 = the seed Put
		t.Errorf("turn_ended produced an extra Put; total = %d, want 1 (seed only)", kv.putCount())
	}
}

// TestLifecycleWatcherDoesNotCreateUnknownAgentRecord covers a forged or
// stale lifecycle envelope for an agent the bucket has never heard of.
// The watcher must NOT create the record.
func TestLifecycleWatcherDoesNotCreateUnknownAgentRecord(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New() // never seeded

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleEnded))

	if _, err := kv.Get(context.Background(), id.String()); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Errorf("unknown-agent envelope created a record; err = %v, want ErrKeyNotFound", err)
	}
}

// TestLifecycleWatcherBumpsVersionOnUpdate — other callers
// (spawn/kill/restart) rely on Version being monotonic. The watcher
// must follow the same convention.
func TestLifecycleWatcherBumpsVersionOnUpdate(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	kv.seedDefinition(t, id, "gamma", sextantproto.LifecycleRunning, 1)

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleEnded))

	def := kv.currentDefinition(t, id)
	if def.Version <= 1 {
		t.Errorf("Version = %d after lifecycle write, want > 1", def.Version)
	}
}

// TestLifecycleWatcherSkipsIdempotentWrite — when the record already
// holds the target state, the watcher must NOT bump Version or rewrite
// the value. Otherwise spawn/restart's authoritative Version stamps get
// churned by repeated lifecycle envelopes.
func TestLifecycleWatcherSkipsIdempotentWrite(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	kv.seedDefinition(t, id, "delta", sextantproto.LifecycleEndedState, 7)

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleEnded))

	def := kv.currentDefinition(t, id)
	if def.Version != 7 {
		t.Errorf("Version = %d, want 7 (idempotent no-op)", def.Version)
	}
	if kv.putCount() != 1 { // 1 = the seed Put
		t.Errorf("idempotent transition produced an extra Put; total = %d, want 1", kv.putCount())
	}
}

// --- helpers ---------------------------------------------------------

func envelopeFor(t *testing.T, id uuid.UUID, transition sextantproto.LifecycleEvent) *nats.Msg {
	t.Helper()
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: id.String()}
	state := sextantproto.IncarnationReady
	switch transition {
	case sextantproto.LifecycleEnded:
		state = sextantproto.IncarnationExited
	case sextantproto.LifecycleCrashedEvent:
		state = sextantproto.IncarnationFailed
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindLifecycle, from,
		sextantproto.LifecyclePayload{
			IncarnationID: uuid.New(),
			AgentUUID:     id,
			Transition:    transition,
			State:         state,
		})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return &nats.Msg{
		Subject: "agents." + id.String() + ".lifecycle",
		Data:    raw,
	}
}

// fakeLifecycleKV satisfies LifecycleDefinitionsKV without a live
// nats-server. Stores raw JSON values keyed by string; counts Puts so
// tests can assert idempotency.
type fakeLifecycleKV struct {
	mu    sync.Mutex
	store map[string][]byte
	puts  uint64
}

func newFakeLifecycleKV() *fakeLifecycleKV {
	return &fakeLifecycleKV{store: map[string][]byte{}}
}

func (f *fakeLifecycleKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.store[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return fakeLifecycleEntry{key: key, value: v}, nil
}

func (f *fakeLifecycleKV) Put(_ context.Context, key string, value []byte) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[key] = append([]byte(nil), value...)
	f.puts++
	return f.puts, nil
}

func (f *fakeLifecycleKV) putCount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

func (f *fakeLifecycleKV) seedDefinition(t *testing.T, id uuid.UUID, name string, state sextantproto.LifecycleState, version uint64) {
	t.Helper()
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      name,
		Type:      "assistant",
		Template:  "default",
		Lifecycle: state,
		Version:   version,
		CreatedAt: sextantproto.NowTimestamp(),
		UpdatedAt: sextantproto.NowTimestamp(),
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	if _, err := f.Put(context.Background(), id.String(), raw); err != nil {
		t.Fatalf("seed put: %v", err)
	}
}

func (f *fakeLifecycleKV) currentDefinition(t *testing.T, id uuid.UUID) sextantproto.AgentDefinition {
	t.Helper()
	entry, err := f.Get(context.Background(), id.String())
	if err != nil {
		t.Fatalf("get def: %v", err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	return def
}

func (f *fakeLifecycleKV) currentLifecycle(t *testing.T, id uuid.UUID) sextantproto.LifecycleState {
	t.Helper()
	return f.currentDefinition(t, id).Lifecycle
}

type fakeLifecycleEntry struct {
	key   string
	value []byte
}

func (e fakeLifecycleEntry) Bucket() string                  { return AgentDefinitionsBucket }
func (e fakeLifecycleEntry) Key() string                     { return e.key }
func (e fakeLifecycleEntry) Value() []byte                   { return e.value }
func (e fakeLifecycleEntry) Revision() uint64                { return 1 }
func (e fakeLifecycleEntry) Created() time.Time              { return time.Time{} }
func (e fakeLifecycleEntry) Delta() uint64                   { return 0 }
func (e fakeLifecycleEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

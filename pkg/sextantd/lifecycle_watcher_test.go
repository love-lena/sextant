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
	if kv.writeCount() != 1 { // 1 = the seed Put
		t.Errorf("turn_ended produced an extra write; total = %d, want 1 (seed only)", kv.writeCount())
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
	if kv.writeCount() != 1 { // 1 = the seed Put
		t.Errorf("idempotent transition produced an extra write; total = %d, want 1", kv.writeCount())
	}
}

// TestLifecycleWatcherDoesNotClobberArchived pins the codex
// adversarial-review fix: once the operator explicitly archived the
// agent, a stale sidecar `ended` envelope from a prior incarnation
// must NOT rewrite the record back to ended. The watcher's
// archive-guard yields to the terminal state.
func TestLifecycleWatcherDoesNotClobberArchived(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	// Pretend archive_agent ran: lifecycle=archived, version=5.
	kv.seedDefinition(t, id, "epsilon", sextantproto.LifecycleArchived, 5)
	writesBefore := kv.writeCount()

	// Stale `ended` envelope from the now-dead prior incarnation.
	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleEnded))

	def := kv.currentDefinition(t, id)
	if def.Lifecycle != sextantproto.LifecycleArchived {
		t.Errorf("Lifecycle = %q, want archived (stale ended must not clobber)", def.Lifecycle)
	}
	if def.Version != 5 {
		t.Errorf("Version = %d, want 5 (archive-guard skips the version bump)", def.Version)
	}
	if kv.writeCount() != writesBefore {
		t.Errorf("write count increased; archive-guard should have skipped the Update")
	}
}

// TestLifecycleWatcherCASRetriesOnConflict pins the retry path: when
// a concurrent writer slips a write in between the watcher's Get and
// Update, the watcher gets ErrKeyExists from Update and retries with
// the fresh revision. On the retry the new state should be applied
// against the updated record.
func TestLifecycleWatcherCASRetriesOnConflict(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	kv.seedDefinition(t, id, "zeta", sextantproto.LifecycleRunning, 1)
	kv.conflictsRemaining = 1 // first Update returns ErrKeyExists

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleEnded))

	if got := kv.currentLifecycle(t, id); got != sextantproto.LifecycleEndedState {
		t.Errorf("Lifecycle = %q, want ended (retry should have succeeded)", got)
	}
	if kv.conflictsRemaining != 0 {
		t.Errorf("conflictsRemaining = %d, want 0 (the test injected exactly 1)", kv.conflictsRemaining)
	}
}

// TestLifecycleWatcherGivesUpAfterPersistentCASConflicts asserts the
// retry budget caps at watcherCASRetries. With more forced conflicts
// than the cap, the watcher logs + drops the envelope rather than
// looping forever.
func TestLifecycleWatcherGivesUpAfterPersistentCASConflicts(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	kv.seedDefinition(t, id, "eta", sextantproto.LifecycleRunning, 1)
	kv.conflictsRemaining = watcherCASRetries + 5 // exceed the budget

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeFor(t, id, sextantproto.LifecycleEnded))

	// The lifecycle did NOT update — every attempt conflicted.
	if got := kv.currentLifecycle(t, id); got != sextantproto.LifecycleRunning {
		t.Errorf("Lifecycle = %q, want running (all retries should have failed)", got)
	}
}

// TestLifecycleWatcherDropsStaleIncarnationTerminal pins the
// stale-incarnation filter Codex flagged on the follow-up review:
// after `transition=started` for incarnation A, an `ended` envelope
// arriving from a prior incarnation B must NOT rewrite the running
// record. Without the filter, restart_agent would be sabotaged by
// its predecessor's final lifecycle envelope.
func TestLifecycleWatcherDropsStaleIncarnationTerminal(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	currentInc := uuid.New()
	staleInc := uuid.New()
	kv.seedDefinition(t, id, "iota", sextantproto.LifecycleRunning, 1)

	w := &LifecycleWatcher{defs: kv}
	// Record the current incarnation via a synthetic `started` envelope.
	w.handle(envelopeForWithIncarnation(t, id, currentInc, sextantproto.LifecycleStarted))
	// Now a stale `ended` from a prior incarnation arrives.
	w.handle(envelopeForWithIncarnation(t, id, staleInc, sextantproto.LifecycleEnded))

	if got := kv.currentLifecycle(t, id); got != sextantproto.LifecycleRunning {
		t.Errorf("Lifecycle = %q, want running (stale ended should have been dropped)", got)
	}
}

// TestLifecycleWatcherAcceptsCurrentIncarnationTerminal — the matching
// incarnation IS applied. Ensures the filter isn't over-broad.
func TestLifecycleWatcherAcceptsCurrentIncarnationTerminal(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	currentInc := uuid.New()
	kv.seedDefinition(t, id, "kappa", sextantproto.LifecycleRunning, 1)

	w := &LifecycleWatcher{defs: kv}
	w.handle(envelopeForWithIncarnation(t, id, currentInc, sextantproto.LifecycleStarted))
	w.handle(envelopeForWithIncarnation(t, id, currentInc, sextantproto.LifecycleEnded))

	if got := kv.currentLifecycle(t, id); got != sextantproto.LifecycleEndedState {
		t.Errorf("Lifecycle = %q, want ended (matching incarnation should apply)", got)
	}
}

// TestLifecycleWatcherWarmUpAllowsFirstEnvelope — daemon-restart case:
// the watcher boots with no incarnation map. The first lifecycle
// envelope for a pre-existing agent should pass through (we don't
// know which incarnation is current, so trust the bus).
func TestLifecycleWatcherWarmUpAllowsFirstEnvelope(t *testing.T) {
	kv := newFakeLifecycleKV()
	id := uuid.New()
	someInc := uuid.New()
	kv.seedDefinition(t, id, "lambda", sextantproto.LifecycleRunning, 1)

	w := &LifecycleWatcher{defs: kv}
	// No started envelope first — first message the watcher sees is ended.
	w.handle(envelopeForWithIncarnation(t, id, someInc, sextantproto.LifecycleEnded))

	if got := kv.currentLifecycle(t, id); got != sextantproto.LifecycleEndedState {
		t.Errorf("Lifecycle = %q, want ended (warm-up: no recorded incarnation, allow)", got)
	}
}

// --- helpers ---------------------------------------------------------

func envelopeFor(t *testing.T, id uuid.UUID, transition sextantproto.LifecycleEvent) *nats.Msg {
	return envelopeForWithIncarnation(t, id, uuid.New(), transition)
}

// envelopeForWithIncarnation lets the caller pin the IncarnationID —
// required by the incarnation-filter tests where the same agent
// receives envelopes from multiple incarnations across the test body.
func envelopeForWithIncarnation(t *testing.T, id, incarnation uuid.UUID, transition sextantproto.LifecycleEvent) *nats.Msg {
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
			IncarnationID: incarnation,
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
// nats-server. Stores raw JSON values + per-key revisions; the Update
// implementation enforces CAS semantics so tests can exercise the
// watcher's retry path. A test that wants to force one conflict
// (simulating a concurrent archive/restart writer slipping in between
// Get and Update) sets conflictsRemaining > 0.
type fakeLifecycleKV struct {
	mu                 sync.Mutex
	store              map[string]fakeLifecycleEntry
	writes             uint64 // counts every storePut + Update
	conflictsRemaining int    // forced CAS-conflict count for retry tests
}

func newFakeLifecycleKV() *fakeLifecycleKV {
	return &fakeLifecycleKV{store: map[string]fakeLifecycleEntry{}}
}

func (f *fakeLifecycleKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.store[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	// Return a copy so the caller can't mutate the stored entry.
	return e, nil
}

// Update is the CAS write. The fake rejects the call when the
// caller's revision doesn't match the stored one — same shape as
// jetstream.KeyValue.Update on a real server. Also drains a forced
// conflict if conflictsRemaining > 0 so tests can pin the retry path.
func (f *fakeLifecycleKV) Update(_ context.Context, key string, value []byte, revision uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conflictsRemaining > 0 {
		f.conflictsRemaining--
		// Bump the stored revision so the next Get reflects the
		// "concurrent writer" that the test simulated.
		if e, ok := f.store[key]; ok {
			e.revision++
			f.store[key] = e
		}
		return 0, jetstream.ErrKeyExists
	}
	existing, ok := f.store[key]
	if !ok {
		return 0, jetstream.ErrKeyNotFound
	}
	if existing.revision != revision {
		return 0, jetstream.ErrKeyExists
	}
	existing.value = append([]byte(nil), value...)
	existing.revision++
	f.store[key] = existing
	f.writes++
	return existing.revision, nil
}

// storePut is a test-only direct write — used by seedDefinition + by
// tests that want to simulate a concurrent archive/restart writer
// updating the record between Get and Update.
func (f *fakeLifecycleKV) storePut(key string, value []byte) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.store[key]
	if !ok {
		existing = fakeLifecycleEntry{key: key, revision: 0}
	}
	existing.value = append([]byte(nil), value...)
	existing.revision++
	f.store[key] = existing
	f.writes++
	return existing.revision
}

// writeCount returns the total number of writes (Update successes +
// storePut calls). Used by idempotency assertions.
func (f *fakeLifecycleKV) writeCount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
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
	f.storePut(id.String(), raw)
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
	key      string
	value    []byte
	revision uint64
}

func (e fakeLifecycleEntry) Bucket() string                  { return AgentDefinitionsBucket }
func (e fakeLifecycleEntry) Key() string                     { return e.key }
func (e fakeLifecycleEntry) Value() []byte                   { return e.value }
func (e fakeLifecycleEntry) Revision() uint64                { return e.revision }
func (e fakeLifecycleEntry) Created() time.Time              { return time.Time{} }
func (e fakeLifecycleEntry) Delta() uint64                   { return 0 }
func (e fakeLifecycleEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

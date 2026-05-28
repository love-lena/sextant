package sextantd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestReconcilerPublishesLostForMissingContainer — 4 KV records:
//   - running-with-container: lifecycle=running, container present
//   - running-without-container: lifecycle=running, container absent
//   - ended: lifecycle=ended (terminal, should be skipped)
//   - archived: lifecycle=archived (terminal, should be skipped)
//
// ContainerLister returns only the running-with-container's container.
// Asserts exactly ONE synthetic envelope was published for the
// running-without-container record.
func TestReconcilerPublishesLostForMissingContainer(t *testing.T) {
	const hostID = "host-abc"

	runWithID := uuid.New()
	runWithInc := uuid.New()
	runWithoutID := uuid.New()
	runWithoutInc := uuid.New()
	endedID := uuid.New()
	archivedID := uuid.New()

	kv := newFakeReconKV()
	kv.seed(t, runWithID, sextantproto.LifecycleRunning, runWithInc)
	kv.seed(t, runWithoutID, sextantproto.LifecycleRunning, runWithoutInc)
	kv.seed(t, endedID, sextantproto.LifecycleEndedState, uuid.Nil)
	kv.seed(t, archivedID, sextantproto.LifecycleArchived, uuid.Nil)

	lister := &fakeListerMgr{containers: []containermgr.ContainerInfo{
		{
			Labels: map[string]string{
				handlers.LabelAgentUUID:     runWithID.String(),
				handlers.LabelIncarnationID: runWithInc.String(),
				handlers.LabelHostID:        hostID,
			},
		},
	}}

	var published []sextantproto.Envelope
	rec := &Reconciler{
		Defs:   kv,
		Mgr:    lister,
		HostID: hostID,
		Publish: func(_ context.Context, env sextantproto.Envelope) error {
			published = append(published, env)
			return nil
		},
	}

	res, err := rec.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(published) != 1 {
		t.Fatalf("published %d envelopes, want 1", len(published))
	}

	env := published[0]
	if env.Kind != sextantproto.KindLifecycle {
		t.Errorf("Kind = %q, want %q", env.Kind, sextantproto.KindLifecycle)
	}

	var payload sextantproto.LifecyclePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.AgentUUID != runWithoutID {
		t.Errorf("AgentUUID = %s, want %s", payload.AgentUUID, runWithoutID)
	}
	if payload.IncarnationID != runWithoutInc {
		t.Errorf("IncarnationID = %s, want %s", payload.IncarnationID, runWithoutInc)
	}
	if payload.Transition != sextantproto.LifecycleLostEvent {
		t.Errorf("Transition = %q, want %q", payload.Transition, sextantproto.LifecycleLostEvent)
	}
	if payload.Source != sextantproto.LifecycleSourceReconciler {
		t.Errorf("Source = %q, want %q", payload.Source, sextantproto.LifecycleSourceReconciler)
	}

	// Sanity-check result counters.
	if res.Scanned != 4 {
		t.Errorf("Scanned = %d, want 4", res.Scanned)
	}
	if res.RunningRecords != 2 {
		t.Errorf("RunningRecords = %d, want 2", res.RunningRecords)
	}
	if res.MissingContainers != 1 {
		t.Errorf("MissingContainers = %d, want 1", res.MissingContainers)
	}
	if res.Published != 1 {
		t.Errorf("Published = %d, want 1", res.Published)
	}
}

// TestReconcilerKeyIncludesIncarnation — verifies the reconciler matches
// on (uuid, incarnation_id) together, not uuid alone. A container
// present for the same agent UUID but a stale incarnation_id must still
// trigger a lost envelope for the current incarnation.
func TestReconcilerKeyIncludesIncarnation(t *testing.T) {
	const hostID = "host-def"

	agentID := uuid.New()
	currentInc := uuid.New()
	staleInc := uuid.New()

	kv := newFakeReconKV()
	kv.seed(t, agentID, sextantproto.LifecycleRunning, currentInc)

	// Container is present but with staleInc, not currentInc.
	lister := &fakeListerMgr{containers: []containermgr.ContainerInfo{
		{
			Labels: map[string]string{
				handlers.LabelAgentUUID:     agentID.String(),
				handlers.LabelIncarnationID: staleInc.String(),
				handlers.LabelHostID:        hostID,
			},
		},
	}}

	var published []sextantproto.Envelope
	rec := &Reconciler{
		Defs:   kv,
		Mgr:    lister,
		HostID: hostID,
		Publish: func(_ context.Context, env sextantproto.Envelope) error {
			published = append(published, env)
			return nil
		},
	}

	_, err := rec.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(published) != 1 {
		t.Fatalf("published %d envelopes, want 1 (stale incarnation must not suppress lost)", len(published))
	}

	var payload sextantproto.LifecyclePayload
	if err := json.Unmarshal(published[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.IncarnationID != currentInc {
		t.Errorf("IncarnationID = %s, want currentInc %s", payload.IncarnationID, currentInc)
	}
	if payload.Transition != sextantproto.LifecycleLostEvent {
		t.Errorf("Transition = %q, want lost", payload.Transition)
	}
}

// --- fakes -----------------------------------------------------------

// fakeReconKV implements ReconcilerDefsKV for reconciler tests. Keyed
// by uuid string.
type fakeReconKV struct {
	entries map[string][]byte
}

func newFakeReconKV() *fakeReconKV {
	return &fakeReconKV{entries: map[string][]byte{}}
}

func (f *fakeReconKV) seed(t *testing.T, id uuid.UUID, state sextantproto.LifecycleState, incID uuid.UUID) {
	t.Helper()
	def := sextantproto.AgentDefinition{
		UUID:                 id,
		Name:                 "agent-" + id.String()[:8],
		Type:                 "assistant",
		Lifecycle:            state,
		CurrentIncarnationID: incID,
		Version:              1,
		CreatedAt:            sextantproto.NowTimestamp(),
		UpdatedAt:            sextantproto.NowTimestamp(),
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	f.entries[id.String()] = raw
}

func (f *fakeReconKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	v, ok := f.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return reconFakeEntry{key: key, value: v}, nil
}

func (f *fakeReconKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	out := make(chan string, len(f.entries))
	for k := range f.entries {
		out <- k
	}
	close(out)
	return reconFakeLister{ch: out}, nil
}

type reconFakeLister struct{ ch chan string }

func (l reconFakeLister) Keys() <-chan string { return l.ch }
func (l reconFakeLister) Stop() error         { return nil }

type reconFakeEntry struct {
	key   string
	value []byte
}

func (e reconFakeEntry) Bucket() string                  { return AgentDefinitionsBucket }
func (e reconFakeEntry) Key() string                     { return e.key }
func (e reconFakeEntry) Value() []byte                   { return e.value }
func (e reconFakeEntry) Revision() uint64                { return 1 }
func (e reconFakeEntry) Created() time.Time              { return time.Time{} }
func (e reconFakeEntry) Delta() uint64                   { return 0 }
func (e reconFakeEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

// fakeListerMgr implements ContainerLister.
type fakeListerMgr struct {
	containers []containermgr.ContainerInfo
}

func (f *fakeListerMgr) List(_ context.Context, _ containermgr.Filter) ([]containermgr.ContainerInfo, error) {
	return f.containers, nil
}

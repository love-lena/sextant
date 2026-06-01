package sextantd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// --- fake KV (ReconcileDefsKV) -------------------------------------------

type fakeEntry struct {
	key string
	val []byte
	rev uint64
}

func (e fakeEntry) Bucket() string                  { return "" }
func (e fakeEntry) Key() string                     { return e.key }
func (e fakeEntry) Value() []byte                   { return e.val }
func (e fakeEntry) Revision() uint64                { return e.rev }
func (e fakeEntry) Created() time.Time              { return time.Time{} }
func (e fakeEntry) Delta() uint64                   { return 0 }
func (e fakeEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

type fakeDefsKV struct {
	mu     sync.Mutex
	vals   map[string][]byte
	revs   map[string]uint64
	writes int // counts successful Update calls (status writes)
}

func newFakeDefsKV() *fakeDefsKV {
	return &fakeDefsKV{vals: map[string][]byte{}, revs: map[string]uint64{}}
}

func (f *fakeDefsKV) put(id uuid.UUID, def sextantproto.AgentDefinition) {
	raw, _ := json.Marshal(def)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[id.String()] = raw
	f.revs[id.String()]++
}

func (f *fakeDefsKV) get(id uuid.UUID) sextantproto.AgentDefinition {
	f.mu.Lock()
	defer f.mu.Unlock()
	var d sextantproto.AgentDefinition
	_ = json.Unmarshal(f.vals[id.String()], &d)
	return d
}

func (f *fakeDefsKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vals[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return fakeEntry{key: key, val: append([]byte(nil), v...), rev: f.revs[key]}, nil
}

func (f *fakeDefsKV) Update(_ context.Context, key string, value []byte, revision uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revs[key] != revision {
		return 0, jetstream.ErrKeyExists
	}
	f.vals[key] = append([]byte(nil), value...)
	f.revs[key]++
	f.writes++
	return f.revs[key], nil
}

func (f *fakeDefsKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.vals))
	for k := range f.vals {
		keys = append(keys, k)
	}
	f.mu.Unlock()
	ch := make(chan string, len(keys))
	for _, k := range keys {
		ch <- k
	}
	close(ch)
	return fakeLister{ch: ch}, nil
}

func (f *fakeDefsKV) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

type fakeLister struct{ ch chan string }

func (l fakeLister) Keys() <-chan string { return l.ch }
func (l fakeLister) Stop() error         { return nil }

// --- fake docker (ContainerObserver) -------------------------------------

type fakeDocker struct {
	mu         sync.Mutex
	containers []containermgr.ContainerInfo
}

func (d *fakeDocker) List(_ context.Context, f containermgr.Filter) ([]containermgr.ContainerInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []containermgr.ContainerInfo
	for _, c := range d.containers {
		match := true
		for k, v := range f.Labels {
			if c.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out, nil
}

func (d *fakeDocker) setRunning(agentID, incID uuid.UUID) {
	// A genuinely-converged running container stamps the SAME fingerprint
	// the fake actuator reports as desired, and the daemon's current epoch —
	// so the steady-state drift verdict is "not drifted."
	d.setRunningStamped(agentID, incID, testFingerprint, sextantproto.WireEpoch)
}

// setRunningStale stamps a running container whose spec-identity labels
// disagree with the fake actuator's desired values — the drift fixture.
func (d *fakeDocker) setRunningStale(agentID, incID uuid.UUID, fingerprint string, epoch int) {
	d.setRunningStamped(agentID, incID, fingerprint, epoch)
}

func (d *fakeDocker) setRunningStamped(agentID, incID uuid.UUID, fingerprint string, epoch int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	labels := map[string]string{
		handlers.LabelAgentUUID:     agentID.String(),
		handlers.LabelIncarnationID: incID.String(),
	}
	if fingerprint != "" {
		labels[handlers.LabelSpecFingerprint] = fingerprint
	}
	if epoch != 0 {
		labels[handlers.LabelWireEpoch] = strconv.Itoa(epoch)
	}
	d.containers = []containermgr.ContainerInfo{{
		ID:     "ctr",
		Status: "running",
		Labels: labels,
	}}
}

func (d *fakeDocker) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.containers = nil
}

// --- fake actuator (reconcileActuator) -----------------------------------

// testFingerprint is the desired spec fingerprint the fake actuator
// reports by default. A genuinely-converged running container (setRunning)
// stamps this same value, so the steady-state drift verdict is "not
// drifted." Drift tests stamp a DIFFERENT value via setRunningStale to
// force a mismatch.
const testFingerprint = "fp-converged"

type fakeActuator struct {
	mu         sync.Mutex
	actuated   int
	stopped    int
	torndown   int
	nextIncTbl []uuid.UUID
	// desiredFP / desiredEpoch are what DesiredFingerprint reports — the
	// "desired" half of the drift compare. Zero value: testFingerprint +
	// the daemon's current WireEpoch (i.e. a converged agent).
	desiredFP    string
	desiredEpoch int
	fpErr        error
	// teardownFailFor is the number of Teardown calls that should FAIL (the
	// injected volume-reclaim fault, bug-ctl-archive-volume-leak). It is
	// decremented on each failing call; once zero, Teardown succeeds. Models
	// "the reclaim keeps failing, then succeeds on a later pass."
	teardownFailFor int
}

func (a *fakeActuator) Actuate(_ context.Context, _ sextantproto.AgentDefinition, _ bool) (handlers.ActuateResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actuated++
	inc := uuid.New()
	a.nextIncTbl = append(a.nextIncTbl, inc)
	return handlers.ActuateResult{IncarnationID: inc, ContainerID: "ctr-" + inc.String()}, nil
}

func (a *fakeActuator) Stop(_ context.Context, _ sextantproto.AgentDefinition) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped++
	return nil
}

func (a *fakeActuator) Teardown(_ context.Context, _ sextantproto.AgentDefinition) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.torndown++
	if a.teardownFailFor > 0 {
		a.teardownFailFor--
		return fmt.Errorf("injected volume-reclaim failure")
	}
	return nil
}

func (a *fakeActuator) DesiredFingerprint(_ context.Context, _ sextantproto.AgentDefinition) (handlers.DesiredSpecID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fpErr != nil {
		return handlers.DesiredSpecID{}, a.fpErr
	}
	fp := a.desiredFP
	if fp == "" {
		fp = testFingerprint
	}
	epoch := a.desiredEpoch
	if epoch == 0 {
		epoch = sextantproto.WireEpoch
	}
	return handlers.DesiredSpecID{Fingerprint: fp, WireEpoch: epoch}, nil
}

func (a *fakeActuator) counts() (int, int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.actuated, a.stopped, a.torndown
}

// --- test harness --------------------------------------------------------

func newTestReconciler(t *testing.T) (*Reconciler, *fakeDefsKV, *fakeDocker, *fakeActuator) {
	t.Helper()
	kv := newFakeDefsKV()
	dk := &fakeDocker{}
	act := &fakeActuator{}
	r := NewReconciler(&Reconciler{
		Defs:       kv,
		Containers: dk,
		Actuator:   act,
		HostID:     "",
		Now:        time.Now,
	})
	return r, kv, dk, act
}

func runningDef(id uuid.UUID, inc uuid.UUID) sextantproto.AgentDefinition {
	return sextantproto.AgentDefinition{
		UUID: id,
		Name: "t",
		Spec: sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
		Status: sextantproto.AgentStatusRecord{
			Observed:             sextantproto.ObservedRunning,
			CurrentIncarnationID: inc,
			ObservedGeneration:   1,
		},
	}
}

// TestReconcile_InitialSpawnActuates: a pending desired=run record drives
// exactly one actuation, and the reconciler stamps the fresh incarnation
// + caught-up generation so the next pass is converged.
func TestReconcile_InitialSpawnActuates(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	kv.put(id, sextantproto.AgentDefinition{
		UUID:   id,
		Name:   "t",
		Spec:   sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
		Status: sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedPending},
	})

	ctx := context.Background()
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	a, _, _ := act.counts()
	if a != 1 {
		t.Fatalf("actuated %d times, want 1", a)
	}
	got := kv.get(id)
	if got.Status.CurrentIncarnationID == uuid.Nil {
		t.Fatalf("incarnation not stamped after actuate")
	}
	if got.Status.ObservedGeneration != 1 {
		t.Fatalf("observed_generation = %d, want 1", got.Status.ObservedGeneration)
	}

	// Simulate the container coming up, then re-reconcile: must NOT actuate
	// again (idempotence) and must converge observed → running.
	dk.setRunning(id, got.Status.CurrentIncarnationID)
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	a, _, _ = act.counts()
	if a != 1 {
		t.Fatalf("re-actuated (actuated=%d); initial spawn not idempotent", a)
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedRunning {
		t.Fatalf("observed = %q, want running", kv.get(id).Status.Observed)
	}
}

// TestReconcile_TwiceIsNoOp: the idempotence oracle (RFC §5.9). A healthy
// running agent reconciled twice does not write the KV the second time
// and never actuates.
func TestReconcile_TwiceIsNoOp(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.setRunning(id, inc)

	ctx := context.Background()
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	w1 := kv.writeCount()
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	w2 := kv.writeCount()
	if w2 != w1 {
		t.Fatalf("second reconcile wrote the KV (%d → %d); not idempotent", w1, w2)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("a healthy agent was actuated %d times; want 0", a)
	}
}

// TestReconcile_OutOfBandKillMarksLost: kill a container out-of-band; the
// reconciler marks it lost on the next pass and does NOT auto-restart
// (auto-recovery is P1).
func TestReconcile_OutOfBandKillMarksLost(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.clear() // container vanished out-of-band; no die hint, no sidecar terminal

	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedLost {
		t.Fatalf("observed = %q, want lost", kv.get(id).Status.Observed)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("lost agent was auto-restarted (actuated=%d); P0 must NOT recover", a)
	}

	// Reconcile again: lost stays lost (no flap).
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("lost agent re-actuated on second pass; want 0")
	}
}

// TestReconcile_SidecarTerminalOutranksLost: a sidecar `ended`/`crashed`
// terminal recorded via OnSidecarLifecycle must outrank a daemon-inferred
// lost — the reconciler does not downgrade it.
func TestReconcile_SidecarTerminalOutranksLost(t *testing.T) {
	r, kv, dk, _ := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	d := runningDef(id, inc)
	kv.put(id, d)
	dk.clear()

	// Sidecar publishes a clean terminal for the current incarnation.
	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID:     id,
		IncarnationID: inc,
		Transition:    sextantproto.LifecycleEnded,
	})
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := kv.get(id).Status.Observed
	if got == sextantproto.ObservedLost {
		t.Fatalf("observed downgraded to lost despite sidecar terminal precedence")
	}
	if got != sextantproto.ObservedEnded {
		t.Fatalf("observed = %q, want ended (sidecar terminal converged)", got)
	}
}

// TestReconcile_DieDebounceSuppressesLost: within the 5s debounce after an
// observed die, the reconciler must NOT mark lost (a clean sidecar `ended`
// may still be in flight). After the window, it converges to lost.
func TestReconcile_DieDebounceSuppressesLost(t *testing.T) {
	kv := newFakeDefsKV()
	dk := &fakeDocker{}
	act := &fakeActuator{}
	clock := &fakeClock{now: time.Now()}
	r := NewReconciler(&Reconciler{
		Defs:        kv,
		Containers:  dk,
		Actuator:    act,
		DieDebounce: 5 * time.Second,
		Now:         clock.Now,
	})
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.clear()

	// Record a die at t0. Within the window, no lost.
	r.mu.Lock()
	r.dieAt[inc] = clock.Now()
	r.mu.Unlock()

	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile within debounce: %v", err)
	}
	if kv.get(id).Status.Observed == sextantproto.ObservedLost {
		t.Fatalf("marked lost inside the 5s die-debounce window")
	}

	// Advance past the debounce; now it converges to lost.
	clock.advance(6 * time.Second)
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile after debounce: %v", err)
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedLost {
		t.Fatalf("observed = %q, want lost after debounce elapsed", kv.get(id).Status.Observed)
	}
}

// TestReconcile_DroppedDieConvergesOnSweep: resilience as a property
// (RFC §5.9). We never deliver a die hint at all — the periodic sweep's
// level reconcile still converges the vanished container to lost.
func TestReconcile_DroppedDieConvergesOnSweep(t *testing.T) {
	r, kv, dk, _ := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.clear() // container gone; the die event was "dropped" (never enqueued)

	// The sweep enqueues every agent; we simulate it by reconciling
	// directly (the sweep's only job is to enqueue, which feeds this).
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("sweep reconcile: %v", err)
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedLost {
		t.Fatalf("dropped die did not converge to lost on the sweep; observed=%q", kv.get(id).Status.Observed)
	}
}

// TestReconcile_StatusWriteDoesNotEnqueue: the spec/status guardrail
// (RFC §5.2) — the reconciler's status write must NOT itself enqueue a
// reconcile (that would be an infinite loop). writeObserved never calls
// Enqueue; this asserts the queue stays empty after a status write.
func TestReconcile_StatusWriteDoesNotEnqueue(t *testing.T) {
	r, kv, dk, _ := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.setRunning(id, inc)

	// Drain anything (queue starts empty).
	if r.queue.Len() != 0 {
		t.Fatalf("queue not empty at start")
	}
	// A reconcile that converges observed=running performs a status write.
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if r.queue.Len() != 0 {
		t.Fatalf("status write enqueued a reconcile (queue len=%d); would infinite-loop", r.queue.Len())
	}
}

// TestReconcile_RestartNonceReactuates: a nonce bump on a running agent
// drives a fresh actuation, and the reconciler stamps observed_nonce so
// the restart converges.
func TestReconcile_RestartNonceReactuates(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	d := runningDef(id, inc)
	d.Spec.ReactuationNonce = 1 // operator bumped the nonce (restart)
	kv.put(id, d)
	dk.setRunning(id, inc) // old incarnation still up

	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("nonce bump did not actuate (actuated=%d), want 1", a)
	}
	got := kv.get(id)
	if got.Status.ObservedNonce != 1 {
		t.Fatalf("observed_nonce = %d, want 1 (restart not converged)", got.Status.ObservedNonce)
	}
}

// TestReconcile_StopAndArchive: desired=paused → Stop; desired=archived →
// Teardown.
func TestReconcile_StopAndArchive(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()

	// Paused with a live container → Stop.
	d := runningDef(id, inc)
	d.Spec.Desired = sextantproto.DesiredPaused
	kv.put(id, d)
	dk.setRunning(id, inc)
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile paused: %v", err)
	}
	if _, s, _ := act.counts(); s != 1 {
		t.Fatalf("paused did not Stop (stopped=%d), want 1", s)
	}

	// Archived with a live container → Teardown.
	d2 := runningDef(id, inc)
	d2.Spec.Desired = sextantproto.DesiredArchived
	kv.put(id, d2)
	dk.setRunning(id, inc)
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile archived: %v", err)
	}
	if _, _, td := act.counts(); td != 1 {
		t.Fatalf("archived did not Teardown (torndown=%d), want 1", td)
	}
}

// TestReconcile_ArchiveReclaimsBeforeFinalizing is the finalizer happy
// path (bug-ctl-archive-volume-leak): a healthy agent with desired=archived
// is torn down (container stopped + volume reclaimed) and the reconciler
// finalizes to the TERMINAL observed=archived — the flip that releases the
// name (NameReleased). Re-archiving the archived agent is then a no-op.
func TestReconcile_ArchiveReclaimsBeforeFinalizing(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()

	d := runningDef(id, inc)
	d.Spec.Desired = sextantproto.DesiredArchived
	kv.put(id, d)
	dk.clear() // container already gone; the reclaim is the only remaining step

	// Pass 1: teardown succeeds → finalize to archived.
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile archive: %v", err)
	}
	if _, _, td := act.counts(); td != 1 {
		t.Fatalf("archive did not Teardown (torndown=%d), want 1", td)
	}
	got := kv.get(id)
	if got.Status.Observed != sextantproto.ObservedArchived {
		t.Fatalf("observed = %q, want archived (terminal flip after confirmed reclaim)", got.Status.Observed)
	}
	if !got.NameReleased() {
		t.Fatalf("NameReleased() = false after finalized archive; name must be reusable now")
	}

	// Pass 2: converged — no further teardown, no KV churn (idempotent).
	w1 := kv.writeCount()
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile archive 2: %v", err)
	}
	if _, _, td := act.counts(); td != 1 {
		t.Fatalf("archived re-torn-down (torndown=%d); archive not idempotent", td)
	}
	if kv.writeCount() != w1 {
		t.Fatalf("steady-state archived reconcile wrote the KV (%d → %d); not idempotent", w1, kv.writeCount())
	}
}

// TestReconcile_ArchiveStaysArchivingOnReclaimFailure is the leak guard
// (bug-ctl-archive-volume-leak): when the per-agent volume reclaim FAILS,
// the reconciler must NOT finalize. It records the intermediate
// observed=archiving (name still held), surfaces the error, and RETRIES
// next pass; once the reclaim succeeds it advances to terminal archived and
// releases the name. The name must NOT be reusable while archiving.
func TestReconcile_ArchiveStaysArchivingOnReclaimFailure(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	act.teardownFailFor = 2 // first two reclaim attempts fail, third succeeds
	id := uuid.New()
	inc := uuid.New()

	d := runningDef(id, inc)
	d.Spec.Desired = sextantproto.DesiredArchived
	kv.put(id, d)
	dk.clear()

	// Pass 1: reclaim fails → archiving, error surfaced (triggers retry).
	if err := r.reconcileOne(context.Background(), id); err == nil {
		t.Fatal("reclaim failure did not surface an error; the reconciler would not retry")
	}
	got := kv.get(id)
	if got.Status.Observed != sextantproto.ObservedArchiving {
		t.Fatalf("observed = %q, want archiving (intermediate, NOT finalized) after a failed reclaim", got.Status.Observed)
	}
	if got.NameReleased() {
		t.Fatal("name released while archiving (reclaim not confirmed); this is the leak the ticket fixes")
	}

	// Pass 2: still failing → still archiving, still held.
	if err := r.reconcileOne(context.Background(), id); err == nil {
		t.Fatal("second reclaim failure did not surface an error")
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedArchiving {
		t.Fatalf("observed = %q, want archiving on the second failed pass", kv.get(id).Status.Observed)
	}

	// Pass 3: reclaim succeeds → finalize to terminal archived, name released.
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile after reclaim recovers: %v", err)
	}
	got = kv.get(id)
	if got.Status.Observed != sextantproto.ObservedArchived {
		t.Fatalf("observed = %q, want archived once the reclaim succeeded", got.Status.Observed)
	}
	if !got.NameReleased() {
		t.Fatal("NameReleased() = false after a confirmed reclaim; name must be reusable now")
	}
	if _, _, td := act.counts(); td != 3 {
		t.Fatalf("Teardown called %d times, want 3 (retry until reclaim confirmed)", td)
	}
}

// fakeClock is an injectable monotonic clock for the debounce test.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

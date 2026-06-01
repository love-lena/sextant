package sextantd

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// --- pure-decision recovery cases ----------------------------------------

// TestDecideRecovery_PolicyAndGates is the truth table for the recovery
// branch (RFC §5.3): the predicate is
//
//	desired=run ∧ observed∈{lost,crashed} ∧ RestartPolicy≠Never
//	  ∧ under budget ∧ backoff elapsed → actuate.
//
// Every gate is asserted independently with the time-dependent verdict
// (recoveryInputs) supplied directly, keeping the decision core clock-free.
func TestDecideRecovery_PolicyAndGates(t *testing.T) {
	t.Parallel()

	run := func(observed sextantproto.ObservedState, policy sextantproto.RestartPolicy, rec recoveryInputs) decision {
		d := def(
			sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1, RestartPolicy: policy},
			sextantproto.AgentStatusRecord{Observed: observed, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
		)
		return decideAction(d, actualState{Recovery: rec})
	}
	elapsed := recoveryInputs{BackoffElapsed: true}

	cases := []struct {
		name       string
		observed   sextantproto.ObservedState
		policy     sextantproto.RestartPolicy
		rec        recoveryInputs
		wantAction actionKind
		wantObs    sextantproto.ObservedState
	}{
		{
			name:     "lost + OnFailure + backoff elapsed -> actuate",
			observed: sextantproto.ObservedLost, policy: sextantproto.RestartOnFailure, rec: elapsed,
			wantAction: actionActuate, wantObs: sextantproto.ObservedPending,
		},
		{
			name:     "crashed + OnFailure + backoff elapsed -> actuate",
			observed: sextantproto.ObservedCrashed, policy: sextantproto.RestartOnFailure, rec: elapsed,
			wantAction: actionActuate, wantObs: sextantproto.ObservedPending,
		},
		{
			name:     "lost + default policy ('' => OnFailure) -> actuate",
			observed: sextantproto.ObservedLost, policy: "", rec: elapsed,
			wantAction: actionActuate,
		},
		{
			name:     "lost + Never -> none (no recovery)",
			observed: sextantproto.ObservedLost, policy: sextantproto.RestartNever, rec: elapsed,
			wantAction: actionNone, wantObs: sextantproto.ObservedLost,
		},
		{
			name:     "clean ended + OnFailure -> NOT restarted",
			observed: sextantproto.ObservedEnded, policy: sextantproto.RestartOnFailure, rec: elapsed,
			wantAction: actionNone, wantObs: sextantproto.ObservedEnded,
		},
		{
			name:     "clean ended + Always -> restarted",
			observed: sextantproto.ObservedEnded, policy: sextantproto.RestartAlways, rec: elapsed,
			wantAction: actionActuate, wantObs: sextantproto.ObservedPending,
		},
		{
			name:     "lost + OnFailure + backoff NOT elapsed -> hold (none)",
			observed: sextantproto.ObservedLost, policy: sextantproto.RestartOnFailure, rec: recoveryInputs{BackoffElapsed: false},
			wantAction: actionNone, wantObs: sextantproto.ObservedLost,
		},
		{
			name:     "lost + OnFailure + budget exhausted -> terminal crashed",
			observed: sextantproto.ObservedLost, policy: sextantproto.RestartOnFailure,
			rec:        recoveryInputs{BackoffElapsed: true, BudgetExhausted: true},
			wantAction: actionNone, wantObs: sextantproto.ObservedCrashed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := run(tc.observed, tc.policy, tc.rec)
			if got.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v", got.Action, tc.wantAction)
			}
			if tc.wantObs != "" && got.Observed != tc.wantObs {
				t.Fatalf("observed = %q, want %q", got.Observed, tc.wantObs)
			}
		})
	}
}

// TestDecideRecovery_PausedAndArchivedNotResurrected: a deliberately
// stopped (paused) or archived agent is NOT auto-restarted regardless of
// the recovery verdict — desired intent wins over a stale observation.
func TestDecideRecovery_PausedAndArchivedNotResurrected(t *testing.T) {
	t.Parallel()
	elapsed := recoveryInputs{BackoffElapsed: true}

	paused := def(
		sextantproto.AgentSpec{Desired: sextantproto.DesiredPaused, Generation: 1, RestartPolicy: sextantproto.RestartAlways},
		sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
	)
	if got := decideAction(paused, actualState{Recovery: elapsed}); got.Action == actionActuate {
		t.Fatalf("paused agent was resurrected (action=%v); intent must win", got.Action)
	}

	archived := def(
		sextantproto.AgentSpec{Desired: sextantproto.DesiredArchived, Generation: 1, RestartPolicy: sextantproto.RestartAlways},
		sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
	)
	if got := decideAction(archived, actualState{Recovery: elapsed}); got.Action == actionActuate {
		t.Fatalf("archived agent was resurrected (action=%v); intent must win", got.Action)
	}
}

// --- backoffFor schedule --------------------------------------------------

// TestBackoffFor_Schedule asserts the exact RFC §8 schedule:
// 10 → 20 → 40 → 80 → 160 → 300 (cap), and stays capped beyond.
func TestBackoffFor_Schedule(t *testing.T) {
	t.Parallel()
	want := []time.Duration{
		10 * time.Second,  // n=1
		20 * time.Second,  // n=2
		40 * time.Second,  // n=3
		80 * time.Second,  // n=4
		160 * time.Second, // n=5
		300 * time.Second, // n=6 (cap)
		300 * time.Second, // n=7 (still capped)
		300 * time.Second, // n=8
	}
	for i, w := range want {
		n := i + 1
		if got := backoffFor(n); got != w {
			t.Fatalf("backoffFor(%d) = %s, want %s", n, got, w)
		}
	}
	// n<=0 is defensive: treat as the initial step.
	if got := backoffFor(0); got != RecoveryBackoffInitial {
		t.Fatalf("backoffFor(0) = %s, want %s", got, RecoveryBackoffInitial)
	}
}

// --- clock-driven end-to-end recovery ------------------------------------

func newRecoveryReconciler(t *testing.T, clock *fakeClock, hb HeartbeatLookup) (*Reconciler, *fakeDefsKV, *fakeDocker, *fakeActuator) {
	t.Helper()
	kv := newFakeDefsKV()
	dk := &fakeDocker{}
	act := &fakeActuator{}
	r := NewReconciler(&Reconciler{
		Defs:       kv,
		Containers: dk,
		Actuator:   act,
		Heartbeats: hb,
		Now:        clock.Now,
	})
	return r, kv, dk, act
}

// TestRecovery_LostAgentSelfHealsAfterBackoff: a lost agent under
// OnFailure must NOT restart inside the 10s backoff, and MUST restart once
// it elapses — fully deterministic under the injected clock.
func TestRecovery_LostAgentSelfHealsAfterBackoff(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, kv, dk, act := newRecoveryReconciler(t, clock, nil)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.clear() // container vanished out-of-band (no die hint, no sidecar terminal)

	ctx := context.Background()

	// Pass 1: marks lost + stamps the backoff anchor (LastExit.At = now).
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedLost {
		t.Fatalf("pass 1 observed = %q, want lost", kv.get(id).Status.Observed)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("pass 1 actuated %d; must wait the backoff first", a)
	}

	// Pass 2: still inside the 10s backoff (advance only 5s) — no restart.
	clock.advance(5 * time.Second)
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("pass 2 actuated %d inside the backoff window; want 0", a)
	}

	// Pass 3: backoff (10s) has now elapsed — restart.
	clock.advance(6 * time.Second) // total 11s > 10s
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("pass 3 actuated %d after backoff elapsed; want 1", a)
	}
	got := kv.get(id)
	if got.Status.RestartCount != 1 {
		t.Fatalf("restart_count = %d, want 1", got.Status.RestartCount)
	}
	if got.Status.CrashWindow.Count != 1 {
		t.Fatalf("crash_window.count = %d, want 1 (one recovery restart)", got.Status.CrashWindow.Count)
	}
}

// TestRecovery_NeverPolicyNeverRestarts: a lost agent with RestartPolicy
// Never is left lost no matter how much time passes.
func TestRecovery_NeverPolicyNeverRestarts(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, kv, dk, act := newRecoveryReconciler(t, clock, nil)
	id := uuid.New()
	inc := uuid.New()
	d := runningDef(id, inc)
	d.Spec.RestartPolicy = sextantproto.RestartNever
	kv.put(id, d)
	dk.clear()

	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := r.reconcileOne(ctx, id); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		clock.advance(5 * time.Minute)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("Never policy auto-restarted (actuated=%d); want 0", a)
	}
	if kv.get(id).Status.Observed != sextantproto.ObservedLost {
		t.Fatalf("observed = %q, want lost (stays lost under Never)", kv.get(id).Status.Observed)
	}
}

// TestRecovery_CrashBudgetTripsToTerminal: a crash-loop trips the budget
// (5 restarts / 10 min) and flips to terminal `crashed`, after which no
// further restarts happen — deterministic under the injected clock. It
// also asserts the backoff schedule grows per restart (10 → 20 → 40 → 80
// → 160), so 5 restarts complete inside the 10-min window.
func TestRecovery_CrashBudgetTripsToTerminal(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, kv, dk, act := newRecoveryReconciler(t, clock, nil)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.clear()
	ctx := context.Background()

	// Each restart cycle: mark lost (or stay lost), wait the growing
	// backoff, restart. The container immediately vanishes again (a tight
	// crash loop), so the agent never reaches a stable run that would reset
	// the window. Drive enough wall-clock that backoff is always satisfied.
	wantBackoff := []time.Duration{10, 20, 40, 80, 160}
	for restart := 0; restart < CrashBudgetLimit; restart++ {
		// Converge to lost (pass that stamps the anchor on the first loop;
		// thereafter the agent is already lost).
		if err := r.reconcileOne(ctx, id); err != nil {
			t.Fatalf("restart %d converge-lost: %v", restart, err)
		}
		// Advance just past the expected backoff for this restart step.
		clock.advance(wantBackoff[restart]*time.Second + time.Second)
		dk.clear() // crash loop: container never comes up
		if err := r.reconcileOne(ctx, id); err != nil {
			t.Fatalf("restart %d actuate: %v", restart, err)
		}
		if a, _, _ := act.counts(); a != restart+1 {
			t.Fatalf("after restart cycle %d: actuated=%d, want %d", restart, a, restart+1)
		}
	}

	// Budget is now spent (count == 5) and the window is still live. The
	// next loss must flip terminal. (Budget is checked BEFORE backoff, so a
	// short advance that keeps the window live is enough — we must NOT
	// advance past CrashBudgetWindow or the window would lapse + reset.)
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("post-budget converge: %v", err)
	}
	clock.advance(30 * time.Second)
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("post-budget reconcile: %v", err)
	}
	got := kv.get(id)
	if got.Status.Observed != sextantproto.ObservedCrashed {
		t.Fatalf("observed = %q, want crashed (budget tripped to terminal)", got.Status.Observed)
	}
	if a, _, _ := act.counts(); a != CrashBudgetLimit {
		t.Fatalf("auto-restarted past the budget (actuated=%d), want %d", a, CrashBudgetLimit)
	}
	if got.Status.RestartCount != CrashBudgetLimit {
		t.Fatalf("restart_count = %d, want %d (monotonic lifetime)", got.Status.RestartCount, CrashBudgetLimit)
	}
}

// TestRecovery_LivenessTripsAfterExactlyThreeFailures: a still-running but
// wedged agent (heartbeat stale) is restarted after exactly 3 consecutive
// failed probes — not before.
func TestRecovery_LivenessTripsAfterExactlyThreeFailures(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	hb := &fakeHeartbeats{}
	r, kv, dk, act := newRecoveryReconciler(t, clock, hb)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.setRunning(id, inc) // container IS running — docker `die` never fires
	ctx := context.Background()

	// A fresh heartbeat far in the past => every probe is stale (wedged).
	hb.set(id, clock.Now().Add(-time.Hour))

	// Probes 1 and 2: stale, but under the 3-failure threshold → no restart.
	for pass := 1; pass <= 2; pass++ {
		clock.advance(LivenessPeriod)
		if err := r.reconcileOne(ctx, id); err != nil {
			t.Fatalf("liveness pass %d: %v", pass, err)
		}
		if a, _, _ := act.counts(); a != 0 {
			t.Fatalf("liveness tripped early after %d failures (actuated=%d), want 0", pass, a)
		}
		if got := kv.get(id).Status.LivenessFailures; got != pass {
			t.Fatalf("liveness_failures = %d after pass %d, want %d", got, pass, pass)
		}
	}

	// Probe 3: the third consecutive failure trips the restart path.
	clock.advance(LivenessPeriod)
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("liveness pass 3: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("liveness did not restart on the 3rd failure (actuated=%d), want 1", a)
	}
	// A liveness restart is a recovery restart — it spends crash budget.
	if got := kv.get(id).Status.CrashWindow.Count; got != 1 {
		t.Fatalf("crash_window.count = %d after liveness restart, want 1", got)
	}
}

// TestRecovery_FreshHeartbeatResetsLivenessCounter: a healthy heartbeat
// between stale probes resets the consecutive-failure counter, so liveness
// only trips on THREE IN A ROW.
func TestRecovery_FreshHeartbeatResetsLivenessCounter(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	hb := &fakeHeartbeats{}
	r, kv, dk, act := newRecoveryReconciler(t, clock, hb)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, runningDef(id, inc))
	dk.setRunning(id, inc)
	ctx := context.Background()

	// Two stale probes…
	hb.set(id, clock.Now().Add(-time.Hour))
	clock.advance(LivenessPeriod)
	_ = r.reconcileOne(ctx, id)
	clock.advance(LivenessPeriod)
	_ = r.reconcileOne(ctx, id)
	if got := kv.get(id).Status.LivenessFailures; got != 2 {
		t.Fatalf("liveness_failures = %d, want 2 before the healthy beat", got)
	}

	// …then a fresh heartbeat resets the counter.
	clock.advance(time.Second)
	hb.set(id, clock.Now())
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("healthy probe reconcile: %v", err)
	}
	if got := kv.get(id).Status.LivenessFailures; got != 0 {
		t.Fatalf("liveness_failures = %d after a healthy beat, want 0", got)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("liveness restarted despite a healthy beat (actuated=%d), want 0", a)
	}
}

// TestRecovery_StableRunResetsCrashWindow: an agent that runs continuously
// for the reset window (RecoveryBackoffReset, an INDEPENDENT constant)
// clears its crash window, so a later transient crash starts fresh.
func TestRecovery_StableRunResetsCrashWindow(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, kv, dk, _ := newRecoveryReconciler(t, clock, nil)
	id := uuid.New()
	inc := uuid.New()
	d := runningDef(id, inc)
	// Seed a non-trivial crash window as if prior restarts happened.
	d.Status.CrashWindow = sextantproto.CrashWindow{Count: 3, Since: sextantproto.AtTimestamp(clock.Now())}
	kv.put(id, d)
	dk.setRunning(id, inc)
	ctx := context.Background()

	// First pass: observes running, stamps RunningSince.
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	if kv.get(id).Status.CrashWindow.Count != 3 {
		t.Fatalf("crash window cleared too early; count = %d, want 3", kv.get(id).Status.CrashWindow.Count)
	}

	// Advance past the stable-run reset window; the next running observation
	// clears the crash budget.
	clock.advance(RecoveryBackoffReset + time.Minute)
	if err := r.reconcileOne(ctx, id); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if got := kv.get(id).Status.CrashWindow.Count; got != 0 {
		t.Fatalf("crash window not reset after a stable run; count = %d, want 0", got)
	}
}

// --- fake heartbeat lookup -----------------------------------------------

type fakeHeartbeats struct {
	mu   sync.Mutex
	last map[uuid.UUID]time.Time
}

func (h *fakeHeartbeats) set(id uuid.UUID, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last = map[uuid.UUID]time.Time{}
	}
	h.last[id] = at
}

func (h *fakeHeartbeats) LastSeen(id uuid.UUID) (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.last[id]
	return t, ok
}

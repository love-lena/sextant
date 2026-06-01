package sextantd

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// driftDef is a caught-up, running desired=run agent — the steady state
// the drift branch evaluates against (generation/nonce already converged,
// so only the fingerprint/epoch compare can fire).
func driftDef(id, inc uuid.UUID) sextantproto.AgentDefinition {
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

// TestReconcile_DriftNoFalsePositive: a healthy, genuinely-converged agent
// (its stamped fingerprint + epoch match the recomputed desired ones) is
// NOT restarted — even parked at a turn boundary. This is the
// no-false-positive bar: we diff OUR recomputed fingerprint, not docker's
// live spec, so a converged agent never looks drifted.
func TestReconcile_DriftNoFalsePositive(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, driftDef(id, inc))
	dk.setRunning(id, inc) // stamps testFingerprint + current WireEpoch

	// Park it at a turn boundary so the ONLY thing that could trigger a
	// restart is a (false) drift verdict.
	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleTurnEnded,
	})

	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("converged agent was restarted (actuated=%d), want 0 — false positive", a)
	}
	if got := kv.get(id); got.Status.Observed != sextantproto.ObservedRunning {
		t.Fatalf("observed = %q, want running", got.Status.Observed)
	}
}

// TestReconcile_DriftEpochSkewConvergesAtBoundary: the daemon-upgrade case
// (RFC §5.8). The running container was stamped with an OLDER wire-epoch
// than the daemon now reports; once the agent reaches a turn boundary the
// reconciler converges it by restart (a fresh incarnation onto the current
// epoch).
func TestReconcile_DriftEpochSkewConvergesAtBoundary(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, driftDef(id, inc))
	// Running on a STALE epoch (current-1); fingerprint still matches.
	dk.setRunningStale(id, inc, testFingerprint, sextantproto.WireEpoch-1)

	// (1) Mid-turn: drift detected but must NOT restart.
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile mid-turn: %v", err)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("epoch-drifted agent restarted mid-turn (actuated=%d), want 0", a)
	}

	// (2) Turn boundary reached → converge by restart.
	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleTurnEnded,
	})
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile at boundary: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("epoch-drifted agent not converged at boundary (actuated=%d), want 1", a)
	}
	got := kv.get(id)
	if got.Status.Observed != sextantproto.ObservedPending {
		t.Fatalf("observed = %q, want pending (restart in flight)", got.Status.Observed)
	}
	// A drift restart is deliberate, not a crash: it must not open/charge the
	// crash budget.
	if got.Status.CrashWindow.Count != 0 {
		t.Fatalf("drift restart charged the crash budget (count=%d), want 0", got.Status.CrashWindow.Count)
	}
}

// TestReconcile_DriftFingerprintConvergesAtBoundary: the spec-edit /
// stale-image case (RFC §5.6). The running container's stamped fingerprint
// no longer matches the recomputed desired one; converge by restart at a
// turn boundary.
func TestReconcile_DriftFingerprintConvergesAtBoundary(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, driftDef(id, inc))
	// Running on a STALE fingerprint (epoch current).
	dk.setRunningStale(id, inc, "fp-stale", sextantproto.WireEpoch)

	// At a turn boundary from the start.
	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleTurnEnded,
	})
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("fingerprint-drifted agent not converged at boundary (actuated=%d), want 1", a)
	}
}

// TestReconcile_DriftMidTurnNotInterrupted: a drifted agent that is
// actively mid-turn (started, no turn_ended yet) is left running. Only
// after turn_ended does the next pass converge it. This is the core
// "never interrupt a turn" guarantee (RFC §5.6).
func TestReconcile_DriftMidTurnNotInterrupted(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, driftDef(id, inc))
	dk.setRunningStale(id, inc, "fp-stale", sextantproto.WireEpoch)

	// Agent is mid-turn (started, never reached a boundary).
	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleStarted,
	})
	for i := 0; i < 3; i++ {
		if err := r.reconcileOne(context.Background(), id); err != nil {
			t.Fatalf("reconcile pass %d: %v", i, err)
		}
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("mid-turn drifted agent was interrupted (actuated=%d), want 0", a)
	}

	// turn_ended arrives → the agent is now safe to converge.
	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleTurnEnded,
	})
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile after turn_ended: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("drifted agent not converged after turn_ended (actuated=%d), want 1", a)
	}
}

// TestReconcile_DriftEditSpecConvergesViaGeneration: the EDIT-a-live-spec
// path (RFC §5.6) — an operator bumps Spec.Generation (e.g. a new image)
// on a running agent. observed_generation < generation forces an immediate
// re-actuation, and the reconciler stamps observed_generation to catch up.
// This converges WITHOUT waiting for a turn boundary (an explicit edit is
// deliberate intent), and observed_generation is what proves it caught up.
func TestReconcile_DriftEditSpecConvergesViaGeneration(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	d := driftDef(id, inc)
	d.Spec.Generation = 2 // operator edited the spec (e.g. bumped the image)
	kv.put(id, d)
	dk.setRunning(id, inc) // old incarnation still up on the old gen

	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if a, _, _ := act.counts(); a != 1 {
		t.Fatalf("spec edit did not re-actuate (actuated=%d), want 1", a)
	}
	got := kv.get(id)
	if got.Status.ObservedGeneration != 2 {
		t.Fatalf("observed_generation = %d, want 2 (reconciler did not catch up to the edit)", got.Status.ObservedGeneration)
	}
	if got.Status.CrashWindow.Count != 0 {
		t.Fatalf("spec-edit re-actuation charged the crash budget (count=%d), want 0", got.Status.CrashWindow.Count)
	}
}

// TestReconcile_DriftRecomputeErrorFailsSafe: if the desired-fingerprint
// recompute errors (transient host-state resolution failure), the
// reconciler must NOT restart a healthy agent — drift fails safe.
func TestReconcile_DriftRecomputeErrorFailsSafe(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, driftDef(id, inc))
	dk.setRunningStale(id, inc, "fp-stale", sextantproto.WireEpoch-1) // would be drift
	act.fpErr = errDriftProbe

	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleTurnEnded,
	})
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if a, _, _ := act.counts(); a != 0 {
		t.Fatalf("recompute error triggered a restart (actuated=%d), want 0 — must fail safe", a)
	}
}

// TestReconcile_DriftTurnBoundaryClearedOnRestart: after a drift restart,
// the fresh incarnation starts mid-turn (no stale turn-boundary carry-over
// from the superseded incarnation), so it is not immediately re-restarted.
func TestReconcile_DriftTurnBoundaryClearedOnRestart(t *testing.T) {
	r, kv, dk, act := newTestReconciler(t)
	id := uuid.New()
	inc := uuid.New()
	kv.put(id, driftDef(id, inc))
	dk.setRunningStale(id, inc, "fp-stale", sextantproto.WireEpoch)

	r.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID: id, IncarnationID: inc, Transition: sextantproto.LifecycleTurnEnded,
	})
	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile (converge): %v", err)
	}
	a, _, _ := act.counts()
	if a != 1 {
		t.Fatalf("first converge did not actuate (actuated=%d), want 1", a)
	}

	// The reconciler stamped a fresh incarnation. Simulate it coming up
	// running but STILL on a stale fingerprint (e.g. the rebuild itself is
	// also stale in this contrived case). Because the fresh incarnation has
	// no turn-boundary flag, a drift verdict must NOT fire until it reaches
	// its own turn_ended.
	freshInc := kv.get(id).Status.CurrentIncarnationID
	d := kv.get(id)
	d.Status.Observed = sextantproto.ObservedRunning
	kv.put(id, d)
	dk.setRunningStale(id, freshInc, "fp-stale", sextantproto.WireEpoch)

	if err := r.reconcileOne(context.Background(), id); err != nil {
		t.Fatalf("reconcile (fresh inc mid-turn): %v", err)
	}
	if a2, _, _ := act.counts(); a2 != 1 {
		t.Fatalf("fresh incarnation re-restarted while mid-turn (actuated=%d), want 1", a2)
	}
}

// errDriftProbe is a sentinel for the recompute-fails-safe test.
var errDriftProbe = drftErr("desired fingerprint recompute boom")

type drftErr string

func (e drftErr) Error() string { return string(e) }

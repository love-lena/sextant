package sextantd

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// deadlineActuator is a reconcileActuator whose Actuate always returns
// context.DeadlineExceeded — modelling the actuator's bounded docker op
// having fired (a wedged dockerd). It proves the reconciler surfaces the
// deadline as a loud `actuate:` error and does NOT swallow it.
type deadlineActuator struct{}

func (deadlineActuator) Actuate(_ context.Context, _ sextantproto.AgentDefinition, _ bool) (handlers.ActuateResult, error) {
	return handlers.ActuateResult{}, context.DeadlineExceeded
}
func (deadlineActuator) Stop(_ context.Context, _ sextantproto.AgentDefinition) error { return nil }
func (deadlineActuator) Teardown(_ context.Context, _ sextantproto.AgentDefinition) error {
	return nil
}

func (deadlineActuator) DesiredFingerprint(_ context.Context, _ sextantproto.AgentDefinition) (handlers.DesiredSpecID, error) {
	return handlers.DesiredSpecID{}, nil
}

// TestReconcile_ActuateDeadline_SurfacesLoudly_NoHang: a pending desired=run
// record whose actuation hits a (bounded) docker deadline must make
// reconcileOnce return a non-nil error wrapping context.DeadlineExceeded —
// surfaced (logged + re-enqueued via the sweep/requeue backstop), never
// swallowed — and it must return PROMPTLY, not hang.
func TestReconcile_ActuateDeadline_SurfacesLoudly_NoHang(t *testing.T) {
	kv := newFakeDefsKV()
	r := NewReconciler(&Reconciler{
		Defs:       kv,
		Containers: &fakeDocker{},
		Actuator:   deadlineActuator{},
		Now:        time.Now,
	})
	id := uuid.New()
	kv.put(id, sextantproto.AgentDefinition{
		UUID:   id,
		Name:   "t",
		Spec:   sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
		Status: sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedPending},
	})

	done := make(chan error, 1)
	go func() { done <- r.reconcileOne(context.Background(), id) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("reconcile swallowed the actuate deadline (got nil error)")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("reconcile err = %v, want wrapped context.DeadlineExceeded", err)
		}
		if !strings.Contains(err.Error(), "actuate") {
			t.Errorf("error %q does not name the actuate op", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcileOne hung on a deadline-returning actuator")
	}
}

// captureLog redirects the standard logger to a buffer for the duration of
// the test, returning the buffer. The watchdog logs via the package logger
// (the daemon's stderr in production), so this is how a test reads its
// LOUD output.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// TestWatchdog_WorkerStall_LogsLoudly: when the worker has been mid-pass on
// one agent past reconcileWorkerStallThreshold, the watchdog logs a LOUD
// "worker stalled on agent <uuid>" — the fail-loud signal for the P1 stall
// (bug-ctl-reconcile-loop-stalls-under-sustained-recovery-churn). Driven
// directly through watchdogCheck under the injected clock so it is
// deterministic (no real time, no goroutine race).
func TestWatchdog_WorkerStall_LogsLoudly(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, _, _, _ := newTestReconciler(t)
	r.Now = clock.Now

	buf := captureLog(t)
	agentID := uuid.New()

	// Worker enters a pass and never leaves (the wedge).
	r.beginPass(agentID)

	var loggedStall uuid.UUID
	var sweepOverdue bool

	// Just under the threshold: no log yet.
	clock.advance(reconcileWorkerStallThreshold - time.Second)
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	if got := buf.String(); strings.Contains(got, "worker stalled") {
		t.Fatalf("logged a stall before the threshold:\n%s", got)
	}

	// Past the threshold: one LOUD line naming the agent.
	clock.advance(2 * time.Second) // now over the threshold
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	got := buf.String()
	if !strings.Contains(got, "worker stalled on agent "+agentID.String()) {
		t.Fatalf("watchdog did not log a worker stall naming %s; log:\n%s", agentID, got)
	}

	// De-dup: a second check while still stalled on the SAME agent does not
	// spam another line.
	buf.Reset()
	clock.advance(time.Second)
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	if got := buf.String(); strings.Contains(got, "worker stalled") {
		t.Fatalf("watchdog re-logged the same stall (should de-dup):\n%s", got)
	}

	// Worker advances → next check clears the de-dup and logs nothing.
	r.endPass()
	clock.advance(time.Second)
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	if loggedStall != uuid.Nil {
		t.Fatalf("stall de-dup not cleared after the worker advanced")
	}
}

// TestWatchdog_SweepOverdue_LogsLoudly: when no sweep has completed within
// SweepOverdueFactor × SweepInterval, the watchdog logs "sweep overdue" —
// the periodic ticker going silent is the exact P1 symptom.
func TestWatchdog_SweepOverdue_LogsLoudly(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, _, _, _ := newTestReconciler(t)
	r.Now = clock.Now
	r.SweepInterval = DefaultSweepInterval

	buf := captureLog(t)

	// Seed a completed sweep at t0.
	r.markSwept()

	var loggedStall uuid.UUID
	var sweepOverdue bool

	// Within the overdue window: no log.
	clock.advance(time.Duration(SweepOverdueFactor)*r.SweepInterval - time.Second)
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	if got := buf.String(); strings.Contains(got, "sweep overdue") {
		t.Fatalf("logged sweep-overdue before the deadline:\n%s", got)
	}

	// Past the window: one LOUD line.
	clock.advance(2 * time.Second)
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	if got := buf.String(); !strings.Contains(got, "sweep overdue by") {
		t.Fatalf("watchdog did not log sweep-overdue; log:\n%s", got)
	}

	// A fresh sweep clears the condition; a later check is quiet again.
	buf.Reset()
	r.markSwept()
	clock.advance(time.Second)
	r.watchdogCheck(&loggedStall, &sweepOverdue)
	if got := buf.String(); strings.Contains(got, "sweep overdue") {
		t.Fatalf("watchdog still logging sweep-overdue after a fresh sweep:\n%s", got)
	}
}

// TestReconcileProgress_Snapshot: Progress() reports last-pass / last-sweep
// ages and the in-flight agent so a daemon health surface can SEE a stall.
func TestReconcileProgress_Snapshot(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0).UTC()}
	r, _, _, _ := newTestReconciler(t)
	r.Now = clock.Now

	r.markSwept()
	r.endPass() // stamps lastProgress

	clock.advance(5 * time.Second)
	p := r.Progress()
	if p.LastPassAge != 5*time.Second {
		t.Errorf("LastPassAge = %s, want 5s", p.LastPassAge)
	}
	if p.LastSweepAge != 5*time.Second {
		t.Errorf("LastSweepAge = %s, want 5s", p.LastSweepAge)
	}
	if p.InFlightAgent != uuid.Nil {
		t.Errorf("InFlightAgent = %s, want Nil (idle)", p.InFlightAgent)
	}

	// Enter a pass; Progress reports the in-flight agent + duration.
	agentID := uuid.New()
	r.beginPass(agentID)
	clock.advance(3 * time.Second)
	p = r.Progress()
	if p.InFlightAgent != agentID {
		t.Errorf("InFlightAgent = %s, want %s", p.InFlightAgent, agentID)
	}
	if p.InFlightFor != 3*time.Second {
		t.Errorf("InFlightFor = %s, want 3s", p.InFlightFor)
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// spawnOnly runs the spawn handler (a desired-state write) WITHOUT
// actuating, returning the new agent's UUID. Used to get a fresh def that
// has no prior live incarnation.
func spawnOnly(t *testing.T, deps handlers.SpawnDeps, name, template string) uuid.UUID {
	t.Helper()
	h := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: name, Template: template,
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var resp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}
	return resp.AgentID
}

// blockingRunner is a ContainerRunner whose Run/Stop block until their
// context is cancelled (then return ctx.Err()) — the wedged-dockerd model:
// a docker SDK call only unblocks when the caller's ctx is cancelled. It
// proves the actuator's per-op deadline makes a wedged op return promptly
// (bug-ctl-reconcile-loop-stalls-under-sustained-recovery-churn) instead
// of hanging the single reconcile worker forever.
type blockingRunner struct {
	ran     chan struct{}
	stopped chan struct{}
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{
		ran:     make(chan struct{}, 1),
		stopped: make(chan struct{}, 1),
	}
}

func (b *blockingRunner) Run(ctx context.Context, _ containermgr.ContainerSpec) (*containermgr.Container, error) {
	select {
	case b.ran <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingRunner) Stop(ctx context.Context, _ string, _ time.Duration) error {
	select {
	case b.stopped <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestActuate_RunTimeout_FailsEarly: a wedged dockerd (Run never returns)
// makes Actuate return a `context deadline exceeded` error promptly —
// bounded by the per-op DockerOpTimeout override — rather than hanging.
// The parent ctx is NOT given a deadline, so this proves the actuator adds
// its OWN deadline (not merely that ctx cancellation propagates).
func TestActuate_RunTimeout_FailsEarly(t *testing.T) {
	deps, _, _, _, _ := buildDeps(t)
	runner := newBlockingRunner()
	adeps := actuatorDepsFrom(deps)
	adeps.Containers = runner
	adeps.DockerOpTimeout = 80 * time.Millisecond
	act := handlers.NewActuator(adeps)

	// Spawn WITHOUT actuating: a fresh def has no prior live incarnation, so
	// Actuate skips the stop-prior step and reaches Run directly — Run is
	// the op under test.
	agentID := spawnOnly(t, deps, "wedged-run", "default")
	def := getDef(t, deps, agentID)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := act.Actuate(context.Background(), def, false)
		done <- err
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Actuate err = %v, want context.DeadlineExceeded", err)
		}
		// "Promptly" — well under any indefinite block. The op timeout is
		// 80ms; allow generous CI slack but assert it did not hang.
		if elapsed > 5*time.Second {
			t.Fatalf("Actuate took %s; expected ~80ms (the per-op deadline) — it hung", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Actuate hung past 10s with a wedged Run — fail-early deadline not applied")
	}
}

// TestStop_StopTimeout_FailsEarly: a wedged dockerd (Stop never returns)
// makes Actuator.Stop return a `context deadline exceeded` error promptly,
// bounded by grace + the (overridden) stop grace buffer. The reconcile
// worker is freed to retry rather than blocking on the single docker call.
func TestStop_StopTimeout_FailsEarly(t *testing.T) {
	deps, _, _, _, _ := buildDeps(t)
	runner := newBlockingRunner()
	adeps := actuatorDepsFrom(deps)
	adeps.Containers = runner
	// grace defaults to 30s; shrink the buffer so the bound is ~grace. To
	// keep the test fast we also rely on the agent's default grace being
	// what the bound adds the buffer to — set a tiny grace via the def.
	adeps.DockerStopGraceBuffer = 50 * time.Millisecond
	act := handlers.NewActuator(adeps)

	// Spawn + actuate so there is a live incarnation for Stop to act on.
	// The blocking runner's Run is bounded by the default DockerOpTimeout,
	// which we do NOT want to wait on during setup — so actuate with a real
	// (non-blocking) runner first, then swap in the blocking one for Stop.
	agentID := spawnAndActuate(t, deps, "wedged-stop", "default")
	def := getDef(t, deps, agentID)
	// GraceSeconds=0 → defaultGraceSeconds (30s) would be too slow; force a
	// tiny grace so the Stop bound is grace(1s)+buffer(50ms) ~= 1s.
	def.Spec.GraceSeconds = 1

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- act.Stop(context.Background(), def)
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop err = %v, want context.DeadlineExceeded", err)
		}
		if elapsed > 5*time.Second {
			t.Fatalf("Stop took %s; expected ~1s (grace+buffer) — it hung", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop hung past 10s with a wedged Stop — fail-early deadline not applied")
	}
}

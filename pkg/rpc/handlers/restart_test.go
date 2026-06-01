package handlers_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Under the declarative model (RFC §5) restart_agent is NOT an actuator:
// it bumps spec.reactuation_nonce, re-asserts desired=run, optionally
// clears the recorded session, and enqueues a reconcile. The reconciler's
// Actuator is the sole thing that stops/starts containers — so these
// tests assert KV + enqueue effects and that the handler touches no
// container runtime. The actuation behavior the OLD restart_test.go
// exercised now lives in pkg/sextantd/reconcile_test.go
// (TestReconcile_RestartNonceReactuates, _StopAndArchive, …) and the
// lossless spec projection in container_spec_test.go.

// captureEnqueuer records the agent ids a handler hints for reconcile.
type captureEnqueuer struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (c *captureEnqueuer) Enqueue(id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
}

func (c *captureEnqueuer) calls() []uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uuid.UUID(nil), c.ids...)
}

// seedDef writes an AgentDefinition straight into the definitions KV with
// the given desired/observed split (and optional recorded session), the
// way an already-actuated agent would look. Returns its uuid.
func seedDef(t *testing.T, defs *fakeMutableKV, name string, desired sextantproto.DesiredState, observed sextantproto.ObservedState, sessionID *string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	def := sextantproto.AgentDefinition{
		UUID:     id,
		Name:     name,
		Type:     "assistant",
		Template: "default",
		Spec: sextantproto.AgentSpec{
			Desired:    desired,
			Generation: 1,
			Runtime:    sextantproto.RuntimeConfig{Model: "claude-opus-4-7[1m]", SessionID: sessionID},
		},
		Status:    sextantproto.AgentStatusRecord{Observed: observed},
		Version:   1,
		CreatedAt: sextantproto.NowTimestamp(),
		UpdatedAt: sextantproto.NowTimestamp(),
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	if _, err := defs.Put(context.Background(), id.String(), raw); err != nil {
		t.Fatalf("seed def: %v", err)
	}
	return id
}

func readDef(t *testing.T, defs *fakeMutableKV, id uuid.UUID) sextantproto.AgentDefinition {
	t.Helper()
	var def sextantproto.AgentDefinition
	raw, ok := defs.snapshot()[id.String()]
	if !ok {
		t.Fatalf("def %s absent from KV", id)
	}
	if err := json.Unmarshal(raw, &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	return def
}

// TestRestartBumpsNonceReassertsRunAndEnqueues is the core declarative
// contract: a restart of a running agent bumps the re-actuation nonce,
// re-asserts desired=run, enqueues exactly one reconcile, and replies OK
// — without touching the container runtime (the reconciler actuates).
func TestRestartBumpsNonceReassertsRunAndEnqueues(t *testing.T) {
	_, defs, incs, runner, _ := buildDeps(t)
	enq := &captureEnqueuer{}
	id := seedDef(t, defs, "alpha", sextantproto.DesiredRun, sextantproto.ObservedRunning, nil)

	h := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
		Enqueue:      enq,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{AgentID: id}), cap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("restart error: %+v", cap.resp.Error)
	}
	var resp sextantproto.RestartAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if !resp.OK || resp.AgentID != id {
		t.Errorf("RestartAgentResponse = %+v, want OK with id %s", resp, id)
	}

	def := readDef(t, defs, id)
	if def.Spec.ReactuationNonce != 1 {
		t.Errorf("ReactuationNonce = %d, want 1 (the restart bump)", def.Spec.ReactuationNonce)
	}
	if def.Spec.Desired != sextantproto.DesiredRun {
		t.Errorf("Desired = %s, want run", def.Spec.Desired)
	}

	if got := enq.calls(); len(got) != 1 || got[0] != id {
		t.Errorf("enqueue calls = %v, want exactly [%s]", got, id)
	}

	// The handler is not an actuator: no container Run/Stop happened.
	runner.mu.Lock()
	specs, stopped := len(runner.specs), len(runner.stopped)
	runner.mu.Unlock()
	if specs != 0 || stopped != 0 {
		t.Errorf("restart touched the runtime: Run=%d Stop=%d, want 0/0 (the reconciler actuates)", specs, stopped)
	}
}

// TestRestartReassertsRunOnPausedAgent: restarting a paused agent means
// "make it run again" — desired flips back to run and the nonce bumps so
// the reconciler builds a fresh incarnation.
func TestRestartReassertsRunOnPausedAgent(t *testing.T) {
	_, defs, incs, runner, _ := buildDeps(t)
	enq := &captureEnqueuer{}
	id := seedDef(t, defs, "beta", sextantproto.DesiredPaused, "", nil)

	h := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions: defs, Incarnations: incs, Containers: runner, Enqueue: enq,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{AgentID: id}), cap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("restart error: %+v", cap.resp.Error)
	}
	def := readDef(t, defs, id)
	if def.Spec.Desired != sextantproto.DesiredRun {
		t.Errorf("Desired = %s, want run (restart re-asserts run on a paused agent)", def.Spec.Desired)
	}
	if def.Spec.ReactuationNonce != 1 {
		t.Errorf("ReactuationNonce = %d, want 1", def.Spec.ReactuationNonce)
	}
}

// TestRestartOnArchivedRefused: restart must refuse an archived agent —
// archived is terminal intent; the operator must spawn a new agent. The
// nonce must not move.
func TestRestartOnArchivedRefused(t *testing.T) {
	_, defs, incs, runner, _ := buildDeps(t)
	enq := &captureEnqueuer{}
	id := seedDef(t, defs, "gamma", sextantproto.DesiredArchived, "", nil)

	h := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions: defs, Incarnations: incs, Containers: runner, Enqueue: enq,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{AgentID: id}), cap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Fatalf("Error = %+v, want bad_request (archived is terminal)", cap.resp.Error)
	}
	def := readDef(t, defs, id)
	if def.Spec.ReactuationNonce != 0 {
		t.Errorf("ReactuationNonce = %d, want 0 (refused restart must not bump)", def.Spec.ReactuationNonce)
	}
	if def.Spec.Desired != sextantproto.DesiredArchived {
		t.Errorf("Desired = %s, want archived (unchanged)", def.Spec.Desired)
	}
	if got := enq.calls(); len(got) != 0 {
		t.Errorf("enqueue calls = %v, want none on a refused restart", got)
	}
}

// TestRestartSessionPreserveContract re-homes bug-restart-preserve-session-noop
// at the spec layer: --preserve-session=true keeps the recorded session id
// on the spec (the actuator/builder then injects SEXTANT_SESSION_ID — see
// container_spec_test.go); --preserve-session=false clears it so the fresh
// incarnation starts clean.
func TestRestartSessionPreserveContract(t *testing.T) {
	sid := "sess_01HXYZRESTARTPRESERVES"

	t.Run("preserve keeps the session", func(t *testing.T) {
		_, defs, incs, runner, _ := buildDeps(t)
		s := sid
		id := seedDef(t, defs, "alpha", sextantproto.DesiredRun, sextantproto.ObservedRunning, &s)
		h := handlers.NewRestartAgent(handlers.RestartDeps{
			Definitions: defs, Incarnations: incs, Containers: runner, Enqueue: &captureEnqueuer{},
		})
		cap := &captureEmit{}
		if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{AgentID: id, PreserveSession: true}), cap.emit()); err != nil {
			t.Fatalf("restart: %v", err)
		}
		if cap.resp.Error != nil {
			t.Fatalf("restart error: %+v", cap.resp.Error)
		}
		def := readDef(t, defs, id)
		if def.Spec.Runtime.SessionID == nil || *def.Spec.Runtime.SessionID != sid {
			t.Errorf("SessionID = %v, want preserved %q", def.Spec.Runtime.SessionID, sid)
		}
	})

	t.Run("no-preserve clears the session", func(t *testing.T) {
		_, defs, incs, runner, _ := buildDeps(t)
		s := sid
		id := seedDef(t, defs, "beta", sextantproto.DesiredRun, sextantproto.ObservedRunning, &s)
		h := handlers.NewRestartAgent(handlers.RestartDeps{
			Definitions: defs, Incarnations: incs, Containers: runner, Enqueue: &captureEnqueuer{},
		})
		cap := &captureEmit{}
		if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{AgentID: id, PreserveSession: false}), cap.emit()); err != nil {
			t.Fatalf("restart: %v", err)
		}
		if cap.resp.Error != nil {
			t.Fatalf("restart error: %+v", cap.resp.Error)
		}
		def := readDef(t, defs, id)
		if def.Spec.Runtime.SessionID != nil {
			t.Errorf("SessionID = %q, want nil (no-preserve starts clean)", *def.Spec.Runtime.SessionID)
		}
	})
}

// TestRestartAgentUnknownAgentReturnsNotFound proves the 404 path.
func TestRestartAgentUnknownAgentReturnsNotFound(t *testing.T) {
	_, defs, incs, runner, _ := buildDeps(t)
	h := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions: defs, Incarnations: incs, Containers: runner, Enqueue: &captureEnqueuer{},
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{AgentID: uuid.New()}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Error = %+v, want agent_not_found", cap.resp.Error)
	}
}

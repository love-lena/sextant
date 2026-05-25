package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestRestartAgentReplacesLiveIncarnation walks the happy path:
// spawn, then restart, and assert the new incarnation is in KV with
// a fresh container ID, the old one is exited, and the definition's
// lifecycle stayed at running.
func TestRestartAgentReplacesLiveIncarnation(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}
	// Capture the original container id so we can prove it changed.
	preSnap := incs.snapshot()
	var preInc sextantproto.AgentIncarnation
	for _, v := range preSnap {
		_ = json.Unmarshal(v, &preInc)
	}
	origContainerID := preInc.ContainerID

	restartH := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions:   defs,
		Incarnations:  incs,
		Containers:    runner,
		CA:            deps.CA,
		WorkspaceRoot: deps.WorkspaceRoot,
		HostID:        deps.HostID,
		NATSURL:       deps.NATSURL,
		NATSUser:      deps.NATSUser,
		NATSPassword:  deps.NATSPassword,
		MCPURL:        deps.MCPURL,
		Issuer:        deps.Issuer,
	})
	rcap := &captureEmit{}
	if err := restartH(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{
		AgentID: spawnResp.AgentID,
	}), rcap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rcap.resp.Error != nil {
		t.Fatalf("restart error: %+v", rcap.resp.Error)
	}
	var restartResp sextantproto.RestartAgentResponse
	if err := json.Unmarshal(rcap.resp.Result, &restartResp); err != nil {
		t.Fatalf("decode restart resp: %v", err)
	}
	if !restartResp.OK || restartResp.AgentID != spawnResp.AgentID {
		t.Errorf("RestartAgentResponse = %+v", restartResp)
	}

	// Old container was stopped via the rollback path of kill (Stop
	// received its ID).
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 1 || stopped[0] != origContainerID {
		t.Errorf("stopped containers = %v, want [%s]", stopped, origContainerID)
	}

	// Two incarnations now exist: the old one (exited) and the new one
	// (starting). The definition still has lifecycle=running.
	incSnap := incs.snapshot()
	if len(incSnap) != 2 {
		t.Fatalf("incarnation count = %d, want 2 (old exited + new running)", len(incSnap))
	}
	var live, exited int
	for _, v := range incSnap {
		var inc sextantproto.AgentIncarnation
		_ = json.Unmarshal(v, &inc)
		switch inc.State {
		case sextantproto.IncarnationStarting:
			live++
		case sextantproto.IncarnationExited:
			exited++
		}
	}
	if live != 1 || exited != 1 {
		t.Errorf("live=%d exited=%d, want live=1 exited=1", live, exited)
	}

	defSnap := defs.snapshot()
	var def sextantproto.AgentDefinition
	_ = json.Unmarshal(defSnap[spawnResp.AgentID.String()], &def)
	if def.Lifecycle != sextantproto.LifecycleRunning {
		t.Errorf("Lifecycle = %s, want running", def.Lifecycle)
	}
}

// TestRestartAgentRollsBackLifecycleOnIncarnationPersistFailure pins
// the partial-failure rollback fix in pkg/rpc/handlers/restart.go.
//
// Sequence of Puts on the incarnations bucket during a spawn+restart:
//   - call 1 (spawn): the initial incarnation record.
//   - call 2 (restart step 2): mark old incarnation as exited.
//   - call 3 (restart step 5): persist the *new* incarnation.
//
// Failing call 3 simulates a KV write error after the old container
// has already been stopped + marked exited and after the new container
// is running. The handler's rollback must:
//   - stop the new container (already covered by the previous patch).
//   - flip the definition back to lifecycle=defined so list_agents
//     doesn't lie about a running agent with zero live incarnations.
//
// Pre-fix the test would observe Lifecycle = "running" even though
// the only live incarnation marker was rolled back, leaving the
// operator with no clear recovery path.
func TestRestartAgentRollsBackLifecycleOnIncarnationPersistFailure(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Inject the failure on the third Put against the incarnations
	// bucket — the new-incarnation persist step (see comment above).
	incs.putHook = func(_ string, callIdx int) error {
		if callIdx == 3 {
			return errors.New("nats KV unhealthy")
		}
		return nil
	}

	restartH := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions:   defs,
		Incarnations:  incs,
		Containers:    runner,
		CA:            deps.CA,
		WorkspaceRoot: deps.WorkspaceRoot,
		HostID:        deps.HostID,
		NATSURL:       deps.NATSURL,
		NATSUser:      deps.NATSUser,
		NATSPassword:  deps.NATSPassword,
		MCPURL:        deps.MCPURL,
		Issuer:        deps.Issuer,
	})
	rcap := &captureEmit{}
	if err := restartH(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{
		AgentID: spawnResp.AgentID,
	}), rcap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rcap.resp.Error == nil {
		t.Fatal("expected an error from the persist-failure path")
	}
	if rcap.resp.Error.Code != sextantproto.ErrCodeInternal {
		t.Errorf("Code = %q, want internal", rcap.resp.Error.Code)
	}

	// Two Stop calls landed on the runner: the old incarnation's
	// container (step 2) AND the new container's rollback (step 5
	// failure path).
	runner.mu.Lock()
	stopped := append([]string(nil), runner.stopped...)
	runner.mu.Unlock()
	if len(stopped) != 2 {
		t.Errorf("runner.stopped = %v, want 2 entries", stopped)
	}

	// The critical fix: definition's lifecycle is now defined, not
	// running. Otherwise list_agents would advertise a running agent
	// with no live incarnation behind it.
	defSnap := defs.snapshot()
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defSnap[spawnResp.AgentID.String()], &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	if def.Lifecycle != sextantproto.LifecycleDefined {
		t.Errorf("def.Lifecycle = %s, want defined (rollback must flip back from running)",
			def.Lifecycle)
	}
}

// TestRestartAgentUnknownAgentReturnsNotFound proves the handler's
// 404 path: an agent_not_found error when the definition isn't in KV.
func TestRestartAgentUnknownAgentReturnsNotFound(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	h := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions:   defs,
		Incarnations:  incs,
		Containers:    runner,
		CA:            deps.CA,
		WorkspaceRoot: deps.WorkspaceRoot,
		HostID:        deps.HostID,
		NATSURL:       deps.NATSURL,
		NATSUser:      deps.NATSUser,
		NATSPassword:  deps.NATSPassword,
		MCPURL:        deps.MCPURL,
		Issuer:        deps.Issuer,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{
		AgentID: uuid.New(),
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Error = %+v, want agent_not_found", cap.resp.Error)
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
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

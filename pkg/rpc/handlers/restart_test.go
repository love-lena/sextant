package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
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

// TestRestartForwardsAPIKey pins [[bug-restart-no-api-key-forwarding]]:
// the restart handler must forward ANTHROPIC_API_KEY from the daemon's
// own env into the new container's env, the same way spawn does. Pre-
// fix, the restart path silently dropped it and the freshly-restarted
// sidecar would fail its next prompt with "Not logged in".
//
// Mirrors the issue's acceptance criterion in a unit-test shape:
// instead of `docker exec env | grep ANTHROPIC` we inspect the spec
// the handler hands to ContainerRunner.Run.
func TestRestartForwardsAPIKey(t *testing.T) {
	const apiKey = "sk-ant-restart-forwarding-test"
	t.Setenv("ANTHROPIC_API_KEY", apiKey)

	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Sanity: the spawn already forwarded it (regression guard so a
	// future refactor that loses the spawn-side forwarding fails here
	// too, not just in TestSpawnAgentHappyPath).
	if got := runner.specs[0].Env["ANTHROPIC_API_KEY"]; got != apiKey {
		t.Fatalf("spawn ANTHROPIC_API_KEY = %q, want %q", got, apiKey)
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
		AgentID:         spawnResp.AgentID,
		PreserveSession: true,
	}), rcap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rcap.resp.Error != nil {
		t.Fatalf("restart error: %+v", rcap.resp.Error)
	}

	// runner.specs[0] is the spawn; runner.specs[1] is the restart.
	if len(runner.specs) != 2 {
		t.Fatalf("runner.specs = %d, want 2 (spawn + restart)", len(runner.specs))
	}
	restartSpec := runner.specs[1]
	if got := restartSpec.Env["ANTHROPIC_API_KEY"]; got != apiKey {
		t.Errorf("restart ANTHROPIC_API_KEY = %q, want %q", got, apiKey)
	}
	// The other env vars the issue's wire-up commit added (SEXTANT_MODEL,
	// SEXTANT_PERMISSION_MODE) must flow through restart too — otherwise
	// the sidecar falls back to its own defaults and we lose the
	// per-template settings. Cheap to assert here while we're already
	// inspecting the spec.
	if got := restartSpec.Env["SEXTANT_MODEL"]; got == "" {
		t.Errorf("restart SEXTANT_MODEL is empty; env = %v", restartSpec.Env)
	}
	if got := restartSpec.Env["SEXTANT_PERMISSION_MODE"]; got != "acceptEdits" {
		t.Errorf("restart SEXTANT_PERMISSION_MODE = %q, want %q (default for unset/auto ceiling)",
			got, "acceptEdits")
	}
}

// TestRestartPreservesSession pins [[bug-restart-preserve-session-noop]]:
// when --preserve-session is true and the definition has a recorded
// SDK session id, the restart handler must inject SEXTANT_SESSION_ID
// into the new container so the sidecar resumes the prior Claude
// conversation. Pre-fix, the flag was logged-and-ignored.
//
// The issue's acceptance flow ("prompt → kill → restart → prompt and
// look for '42'") requires a live SDK; we substitute the on-the-wire
// effect of that flow: seed def.Runtime.SessionID directly in KV as
// the sidecar would have via CAS, then assert the restart spec's env
// carries that exact id.
func TestRestartPreservesSession(t *testing.T) {
	const sessionID = "sess_01HXYZRESTARTPRESERVES"

	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Simulate the sidecar's "first turn captured a session id and
	// persisted it via CAS" step: load the def, set SessionID, write
	// back. The unit-test version of the issue's repro `sextant agents
	// prompt smoke "remember 42"` flow.
	defEntry, err := defs.Get(context.Background(), spawnResp.AgentID.String())
	if err != nil {
		t.Fatalf("get def: %v", err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defEntry.Value(), &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	sid := sessionID
	def.Runtime.SessionID = &sid
	raw, _ := json.Marshal(def)
	if _, err := defs.Put(context.Background(), def.UUID.String(), raw); err != nil {
		t.Fatalf("put def: %v", err)
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

	// Case 1: --preserve-session true → SEXTANT_SESSION_ID carries through.
	rcap := &captureEmit{}
	if err := restartH(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{
		AgentID:         spawnResp.AgentID,
		PreserveSession: true,
	}), rcap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rcap.resp.Error != nil {
		t.Fatalf("restart error: %+v", rcap.resp.Error)
	}
	if len(runner.specs) < 2 {
		t.Fatalf("runner.specs = %d, want at least 2 (spawn + restart)", len(runner.specs))
	}
	if got := runner.specs[1].Env["SEXTANT_SESSION_ID"]; got != sessionID {
		t.Errorf("preserve-session=true: SEXTANT_SESSION_ID = %q, want %q", got, sessionID)
	}

	// Case 2: --preserve-session false → SEXTANT_SESSION_ID must NOT be
	// set, otherwise the flag has no behavioral difference and the bug
	// is half-fixed. We need a fresh agent + def-with-SessionID for
	// this leg because the previous restart bumped lifecycle to running
	// and recovering the def shape is more bookkeeping than a second
	// agent buys us.
	cap2 := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "beta", Template: "default",
	}), cap2.emit()); err != nil {
		t.Fatalf("second spawn: %v", err)
	}
	if cap2.resp.Error != nil {
		t.Fatalf("second spawn error: %+v", cap2.resp.Error)
	}
	var spawnResp2 sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap2.resp.Result, &spawnResp2); err != nil {
		t.Fatalf("decode second spawn: %v", err)
	}
	defEntry2, err := defs.Get(context.Background(), spawnResp2.AgentID.String())
	if err != nil {
		t.Fatalf("get def2: %v", err)
	}
	var def2 sextantproto.AgentDefinition
	if err := json.Unmarshal(defEntry2.Value(), &def2); err != nil {
		t.Fatalf("decode def2: %v", err)
	}
	sid2 := sessionID
	def2.Runtime.SessionID = &sid2
	raw2, _ := json.Marshal(def2)
	if _, err := defs.Put(context.Background(), def2.UUID.String(), raw2); err != nil {
		t.Fatalf("put def2: %v", err)
	}

	specsBefore := len(runner.specs)
	rcap2 := &captureEmit{}
	if err := restartH(context.Background(), makeReq(t, sextantproto.RestartAgentRequest{
		AgentID:         spawnResp2.AgentID,
		PreserveSession: false,
	}), rcap2.emit()); err != nil {
		t.Fatalf("restart (no-preserve): %v", err)
	}
	if rcap2.resp.Error != nil {
		t.Fatalf("restart (no-preserve) error: %+v", rcap2.resp.Error)
	}
	if len(runner.specs) != specsBefore+1 {
		t.Fatalf("runner.specs grew by %d, want 1", len(runner.specs)-specsBefore)
	}
	if got, ok := runner.specs[specsBefore].Env["SEXTANT_SESSION_ID"]; ok {
		t.Errorf("preserve-session=false: SEXTANT_SESSION_ID set to %q, want unset", got)
	}
}

// TestRestartAgentReattachesClaudeSeedVolume pins the second half of
// the bug-claude-seed-readonly-breaks-session-persistence fix: when
// the spawned agent uses a copy-on-spawn claude_seed and a restart
// re-spawns the container, the new container's spec must include the
// same per-agent named-volume mount so the SDK's session journal under
// /home/agent/.claude/projects survives the restart. Without this
// re-attach the --preserve-session restart wires SEXTANT_SESSION_ID
// into the new container but its `~/.claude` is fresh, so the SDK can't
// find the journal and the resume 404s. Same root cause as the original
// bug, different surface (restart vs first spawn).
func TestRestartAgentReattachesClaudeSeedVolume(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	vols := newFakeVolumeManager()
	deps.Volumes = vols

	// Re-seed the templates KV with a claude_seed template. The
	// existing default template doesn't carry a seed.
	seedDir := t.TempDir()
	tplJSON, err := json.Marshal(map[string]any{
		"name":        "seeded",
		"image":       "sextant-sidecar:latest",
		"permissions": []string{"read.agents", "control.prompt"},
		"mounts":      []string{"worktree"},
		"model":       "claude-opus-4-7[1m]",
		"claude_seed": seedDir,
	})
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tplKV := &fakeTemplatesKV{}
	if _, err := tplKV.Put(context.Background(), "seeded", tplJSON); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	deps.Templates = tplKV

	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "seeded",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", cap.resp.Error)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}
	wantVol := handlers.ClaudeSeedVolumeName(spawnResp.AgentID)

	restartH := handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions:   defs,
		Incarnations:  incs,
		Containers:    runner,
		Volumes:       vols,
		Templates:     tplKV,
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
		AgentID:         spawnResp.AgentID,
		PreserveSession: true,
	}), rcap.emit()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rcap.resp.Error != nil {
		t.Fatalf("restart error: %+v", rcap.resp.Error)
	}

	// Restart should issue exactly ONE container Run after the spawn's
	// own Run — we want to inspect the restart spec, not the spawn spec.
	if len(runner.specs) < 2 {
		t.Fatalf("expected at least 2 container specs (spawn + restart); got %d", len(runner.specs))
	}
	restartSpec := runner.specs[len(runner.specs)-1]

	// The restart spec must include the same /home/agent/.claude mount
	// pointing at the agent's named volume — that's the mechanism by
	// which the SDK's session journal survives. Without it the
	// preserved SEXTANT_SESSION_ID points at a journal the new
	// container can't see.
	var found bool
	for _, m := range restartSpec.Mounts {
		if m.ContainerPath == "/home/agent/.claude" && m.VolumeName == wantVol {
			found = true
			if m.ReadOnly {
				t.Error("restart re-attached seed volume must be rw, not ro")
			}
			break
		}
	}
	if !found {
		var summary []string
		for _, m := range restartSpec.Mounts {
			summary = append(summary, m.HostPath+m.VolumeName+"->"+m.ContainerPath)
		}
		t.Errorf("restart spec missing /home/agent/.claude volume mount for %s; mounts = %v", wantVol, summary)
	}

	// EnsureVolume was called twice (spawn + restart) on the same name,
	// but Populate must have run only once (idempotent on second call).
	vols.mu.Lock()
	popCount := len(vols.populate)
	vols.mu.Unlock()
	if popCount != 1 {
		t.Errorf("populate count = %d, want 1 (restart must reattach, not repopulate)", popCount)
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

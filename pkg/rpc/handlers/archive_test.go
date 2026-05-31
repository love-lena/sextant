package handlers_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestArchiveAgentRespectsConcurrentRestart pins the symmetric race to
// TestRestartAgentRespectsConcurrentArchive: if a concurrent writer (the
// reconciler's status write, or a racing restart) bumps the definition's
// revision between archive's initial Get and its final CAS write, archive
// must refuse to overwrite the racer's state with desired=archived rather
// than silently clobbering it.
//
// Under the declarative model archive_agent is a desired-state edit
// (spec.desired=archived via CAS); the teardown is the reconciler's job
// (sole actuator). The race is injected on the def-key Update path: a
// `bumpingKV` wrapper bumps the revision before every Update, guaranteeing
// archive's CAS budget exhausts and it surfaces BAD_REQUEST.
func TestArchiveAgentRespectsConcurrentRestart(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)

	spawnH := handlers.NewSpawnAgent(deps)
	cap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "delta", Template: "default",
	}), cap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// Every archive CAS attempt collides with a concurrent revision bump.
	bumpingDefs := &bumpingKV{wrapped: defs, key: spawnResp.AgentID.String()}

	archiveH := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  bumpingDefs,
		Incarnations: incs,
		Containers:   runner,
	})
	acap := &captureEmit{}
	if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: spawnResp.AgentID,
	}), acap.emit()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if acap.resp.Error == nil {
		t.Fatal("expected an error — archive must refuse to overwrite the concurrent writer")
	}
	if acap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("Error.Code = %q, want %q (CAS conflict surfaces as bad_request)",
			acap.resp.Error.Code, sextantproto.ErrCodeBadRequest)
	}

	// Definition must NOT have flipped to archived — the CAS guard kept
	// archive from clobbering the concurrent writer's record.
	defSnap := defs.snapshot()
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(defSnap[spawnResp.AgentID.String()], &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	if def.Spec.Desired == sextantproto.DesiredArchived {
		t.Errorf("def.Spec.Desired = %s, want NOT archived (archive must not clobber the concurrent writer)",
			def.Spec.Desired)
	}
}

// TestArchiveAgentReleasesName pins the full bundle that the
// bug-kill-doesnt-release-name + feat-agents-archive-cli-verb pair was
// filed to solve: an operator can spawn `foo`, kill `foo`, archive
// `foo`, then spawn `foo` again without colliding on the name. Without
// the archive step the second spawn fails because spawn.agentNameInUse
// would see the killed-but-non-archived record and refuse the duplicate.
func TestArchiveAgentReleasesName(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	killH := handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	archiveH := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})

	// 1) Spawn agent-foo.
	cap1 := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "agent-foo", Template: "default",
	}), cap1.emit()); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if cap1.resp.Error != nil {
		t.Fatalf("first spawn error: %+v", cap1.resp.Error)
	}
	var spawnResp1 sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap1.resp.Result, &spawnResp1); err != nil {
		t.Fatalf("decode first spawn: %v", err)
	}

	// 2) Kill agent-foo. Desired goes to paused (not archived); the name
	// is still held.
	killCap := &captureEmit{}
	if err := killH(context.Background(), makeReq(t, sextantproto.KillAgentRequest{
		AgentID: spawnResp1.AgentID,
	}), killCap.emit()); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if killCap.resp.Error != nil {
		t.Fatalf("kill error: %+v", killCap.resp.Error)
	}

	// Sanity: with the bug present, re-spawning fails here. We assert
	// that pre-archive state to keep the regression guard tight.
	collisionCap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "agent-foo", Template: "default",
	}), collisionCap.emit()); err != nil {
		t.Fatalf("collision spawn: %v", err)
	}
	if collisionCap.resp.Error == nil {
		t.Fatalf("expected name-in-use error before archive; spawn succeeded — the bug is back")
	}
	if collisionCap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("collision error code = %q, want %q",
			collisionCap.resp.Error.Code, sextantproto.ErrCodeBadRequest)
	}

	// 3) Archive agent-foo. spec.desired flips to archived.
	archiveCap := &captureEmit{}
	if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: spawnResp1.AgentID,
	}), archiveCap.emit()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archiveCap.resp.Error != nil {
		t.Fatalf("archive error: %+v", archiveCap.resp.Error)
	}
	defSnap := defs.snapshot()
	var archivedDef sextantproto.AgentDefinition
	if err := json.Unmarshal(defSnap[spawnResp1.AgentID.String()], &archivedDef); err != nil {
		t.Fatalf("decode archived def: %v", err)
	}
	if archivedDef.Spec.Desired != sextantproto.DesiredArchived {
		t.Errorf("spec.desired after archive = %s, want archived", archivedDef.Spec.Desired)
	}
	if archivedDef.Lifecycle() != sextantproto.LifecycleArchived {
		t.Errorf("Lifecycle() after archive = %s, want archived", archivedDef.Lifecycle())
	}

	// 4) Spawn agent-foo again — succeeds. This is the acceptance
	// criterion stated in bug-kill-doesnt-release-name.md.
	cap2 := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "agent-foo", Template: "default",
	}), cap2.emit()); err != nil {
		t.Fatalf("post-archive spawn: %v", err)
	}
	if cap2.resp.Error != nil {
		t.Fatalf("post-archive spawn error: %+v", cap2.resp.Error)
	}
	var spawnResp2 sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(cap2.resp.Result, &spawnResp2); err != nil {
		t.Fatalf("decode post-archive spawn: %v", err)
	}
	if spawnResp2.AgentID == spawnResp1.AgentID {
		t.Errorf("post-archive spawn returned the same UUID; a fresh agent must get a new UUID")
	}
}

// TestArchiveAgentOnRunningAgentSetsArchivedIntent covers the "archive a
// running agent" path: under the declarative model archive writes
// spec.desired=archived (the reconciler tears the live container down +
// reclaims the volume out-of-band). This is the shape the MCP path uses
// when an agent caller archives its own child without an explicit kill.
func TestArchiveAgentOnRunningAgentSetsArchivedIntent(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	archiveH := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})

	spawnCap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), spawnCap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(spawnCap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	archiveCap := &captureEmit{}
	if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: spawnResp.AgentID,
	}), archiveCap.emit()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archiveCap.resp.Error != nil {
		t.Fatalf("archive error: %+v", archiveCap.resp.Error)
	}

	// Definition records archived intent. The reconciler converges the
	// teardown (container stop + volume reclaim) — see the reconciler
	// convergence + e2e tests, not this handler-level test.
	var def sextantproto.AgentDefinition
	_ = json.Unmarshal(defs.snapshot()[spawnResp.AgentID.String()], &def)
	if def.Spec.Desired != sextantproto.DesiredArchived {
		t.Errorf("spec.desired = %s, want archived", def.Spec.Desired)
	}
	if def.Lifecycle() != sextantproto.LifecycleArchived {
		t.Errorf("Lifecycle() = %s, want archived", def.Lifecycle())
	}
}

// TestArchiveAgentIdempotent confirms archiving an already-archived
// agent is a successful no-op rather than an error. Operators that
// retry an `archive_agent` call after a transient failure shouldn't
// have to reason about "did the first attempt succeed?".
func TestArchiveAgentIdempotent(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	archiveH := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})

	spawnCap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "alpha", Template: "default",
	}), spawnCap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(spawnCap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// First archive: real work.
	first := &captureEmit{}
	if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: spawnResp.AgentID,
	}), first.emit()); err != nil {
		t.Fatalf("first archive: %v", err)
	}
	if first.resp.Error != nil {
		t.Fatalf("first archive error: %+v", first.resp.Error)
	}

	// Second archive: idempotent.
	second := &captureEmit{}
	if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: spawnResp.AgentID,
	}), second.emit()); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	if second.resp.Error != nil {
		t.Fatalf("second archive returned error: %+v", second.resp.Error)
	}
}

// TestKillWithArchiveFlag mirrors the `sextant agents stop --archive`
// CLI flow at the handler level: spawn_agent → kill_agent → archive_agent,
// then assert the name is immediately reusable. The CLI flag pairs two
// RPCs (kill_agent followed by archive_agent) — this test verifies the
// daemon behaviour the flag relies on without needing a running CLI
// binary. The wire RPC verb names (`spawn_agent`, `kill_agent`) are
// unchanged by the closed-exception CLI verb rename; only the user-
// facing CLI verbs moved (`spawn`→`create`, `kill`→`stop`).
//
// Per plans/issues/bug-kill-doesnt-release-name.md Option A.
func TestKillWithArchiveFlag(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	killH := handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	archiveH := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})

	// spawn
	spawnCap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "smoke", Template: "default",
	}), spawnCap.emit()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if spawnCap.resp.Error != nil {
		t.Fatalf("spawn error: %+v", spawnCap.resp.Error)
	}
	var spawnResp sextantproto.SpawnAgentResponse
	if err := json.Unmarshal(spawnCap.resp.Result, &spawnResp); err != nil {
		t.Fatalf("decode spawn: %v", err)
	}

	// kill — same shape the CLI sends.
	killCap := &captureEmit{}
	if err := killH(context.Background(), makeReq(t, sextantproto.KillAgentRequest{
		AgentID: spawnResp.AgentID, GraceSeconds: 5,
	}), killCap.emit()); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if killCap.resp.Error != nil {
		t.Fatalf("kill error: %+v", killCap.resp.Error)
	}

	// archive — the second leg of the --archive flag.
	archCap := &captureEmit{}
	if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: spawnResp.AgentID,
	}), archCap.emit()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archCap.resp.Error != nil {
		t.Fatalf("archive error: %+v", archCap.resp.Error)
	}

	// Re-spawn with the same name — succeeds.
	reCap := &captureEmit{}
	if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
		Name: "smoke", Template: "default",
	}), reCap.emit()); err != nil {
		t.Fatalf("re-spawn: %v", err)
	}
	if reCap.resp.Error != nil {
		t.Fatalf("re-spawn error: %+v (the --archive flag's promise is broken)", reCap.resp.Error)
	}
}

// TestArchiveAllDead mirrors `sextant agents archive --all-dead`: spawn
// three agents, kill them all, then archive every agent currently paused.
// After the bulk run, list_agents must report zero non-archived agents
// and every name must be reusable.
//
// The CLI's --all-dead loop is `list_agents(filter=paused)` →
// `archive_agent(uuid)` per row; this test exercises the same shape so
// the bulk flow is regression-tested without a running daemon.
func TestArchiveAllDead(t *testing.T) {
	deps, defs, incs, runner, _ := buildDeps(t)
	spawnH := handlers.NewSpawnAgent(deps)
	listH := handlers.NewListAgents(defs)
	killH := handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	archiveH := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})

	names := []string{"alpha", "beta", "gamma"}
	ids := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		cap := &captureEmit{}
		if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
			Name: n, Template: "default",
		}), cap.emit()); err != nil {
			t.Fatalf("spawn %s: %v", n, err)
		}
		if cap.resp.Error != nil {
			t.Fatalf("spawn %s error: %+v", n, cap.resp.Error)
		}
		var sr sextantproto.SpawnAgentResponse
		if err := json.Unmarshal(cap.resp.Result, &sr); err != nil {
			t.Fatalf("decode spawn %s: %v", n, err)
		}
		ids = append(ids, sr.AgentID)
	}

	// Kill all 3 — every agent now sits at desired=paused (lifecycle
	// projects to paused).
	for _, id := range ids {
		cap := &captureEmit{}
		if err := killH(context.Background(), makeReq(t, sextantproto.KillAgentRequest{
			AgentID: id,
		}), cap.emit()); err != nil {
			t.Fatalf("kill %s: %v", id, err)
		}
		if cap.resp.Error != nil {
			t.Fatalf("kill %s error: %+v", id, cap.resp.Error)
		}
	}

	// list_agents(filter=paused) — the same query --all-dead runs.
	listCap := &captureEmit{}
	if err := listH(context.Background(), makeReq(t, sextantproto.ListAgentsRequest{
		Filter: &sextantproto.ListAgentsFilter{Lifecycle: string(sextantproto.LifecyclePaused)},
	}), listCap.emit()); err != nil {
		t.Fatalf("list_agents: %v", err)
	}
	var listResp sextantproto.ListAgentsResponse
	if err := json.Unmarshal(listCap.resp.Result, &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Agents) != len(names) {
		t.Fatalf("paused agents = %d, want %d", len(listResp.Agents), len(names))
	}

	// Archive each.
	for _, a := range listResp.Agents {
		cap := &captureEmit{}
		if err := archiveH(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
			AgentID: a.UUID,
		}), cap.emit()); err != nil {
			t.Fatalf("archive %s: %v", a.UUID, err)
		}
		if cap.resp.Error != nil {
			t.Fatalf("archive %s error: %+v", a.UUID, cap.resp.Error)
		}
	}

	// After the bulk archive, list_agents(filter=paused) returns 0,
	// and every name is reusable.
	postCap := &captureEmit{}
	if err := listH(context.Background(), makeReq(t, sextantproto.ListAgentsRequest{
		Filter: &sextantproto.ListAgentsFilter{Lifecycle: string(sextantproto.LifecyclePaused)},
	}), postCap.emit()); err != nil {
		t.Fatalf("list_agents post-archive: %v", err)
	}
	var postResp sextantproto.ListAgentsResponse
	if err := json.Unmarshal(postCap.resp.Result, &postResp); err != nil {
		t.Fatalf("decode post-list: %v", err)
	}
	if len(postResp.Agents) != 0 {
		t.Errorf("paused agents post --all-dead = %d, want 0", len(postResp.Agents))
	}

	// Spawn each name again to prove the names were truly released.
	for _, n := range names {
		cap := &captureEmit{}
		if err := spawnH(context.Background(), makeReq(t, sextantproto.SpawnAgentRequest{
			Name: n, Template: "default",
		}), cap.emit()); err != nil {
			t.Fatalf("re-spawn %s: %v", n, err)
		}
		if cap.resp.Error != nil {
			t.Errorf("re-spawn %s error: %+v (name %q still claimed after --all-dead)",
				n, cap.resp.Error, n)
		}
	}
}

// TestArchiveAgentUnknownReturnsNotFound proves the 404 path.
func TestArchiveAgentUnknownReturnsNotFound(t *testing.T) {
	_, defs, incs, runner, _ := buildDeps(t)
	h := handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ArchiveAgentRequest{
		AgentID: uuid.New(),
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Error = %+v, want agent_not_found", cap.resp.Error)
	}
}

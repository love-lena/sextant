package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// recovery_e2e_test.go is the P1 recovery acceptance e2e (real daemon +
// docker), feat-ctl-p1-recovery. It exercises the three operator-visible
// recovery paths against a live sidecar:
//
//   - kill a container out-of-band → the reconciler auto-restarts it
//     (resuming from the persisted session) and the RESTARTS count climbs;
//   - crash-loop a container → the crash budget (5 / 10 min) trips and the
//     agent parks in terminal `crashed`, surfaced by list_agents;
//   - wedge a still-running agent (no heartbeat) → liveness restarts it.
//
// It is timing-heavy (real backoff is 10s ×2) and docker-backed, so it is
// for CI — it self-skips when docker / the sidecar image are absent, like
// the other e2e tests in this package. Do NOT run it on the watchdog'd
// local path; CI's sidecar job runs it.

// pollAgentStatus fetches the agent's status, failing the test on RPC
// error. Used to read the operator-visible Lifecycle + RESTARTS surface.
func pollAgentStatus(t *testing.T, cli *client.Client, agentID uuid.UUID) sextantproto.AgentStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var resp sextantproto.GetAgentStatusResponse
	if err := cli.RPC(ctx, rpc.VerbGetAgentStatus,
		sextantproto.GetAgentStatusRequest{AgentID: agentID}, &resp); err != nil {
		t.Fatalf("get_agent_status: %v", err)
	}
	return resp.Status
}

// dockerKill force-kills (out-of-band) the first container carrying the
// agent label — the OOM / hard-kill the docker `die` watcher + reconciler
// must recover from. Best-effort; the caller polls for the consequence.
func dockerKill(dockerBin string, agentID uuid.UUID) {
	out, _ := exec.Command(dockerBin, "ps", //nolint:gosec // test-controlled args
		"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
		"--format", "{{.ID}}").Output()
	for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
		_ = exec.Command(dockerBin, "kill", id).Run() //nolint:gosec // test-controlled args
	}
}

// TestRecovery_E2E_KillRestartsAndSurfacesRestartCount: kill the
// container out-of-band; the reconciler auto-restarts a fresh incarnation
// and the RESTARTS count (RFC §2 operator surface) climbs.
func TestRecovery_E2E_KillRestartsAndSurfacesRestartCount(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)
	ctx := context.Background()

	spawnCtx, spawnCancel := context.WithTimeout(ctx, 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "recover-kill",
		Template: "default",
	}, &spawnResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	firstContainer := waitForContainer(t, dockerBin, agentID, 30*time.Second)

	// Kill the container out-of-band (OOM / hard-kill — no sidecar terminal).
	dockerKill(dockerBin, agentID)

	// The reconciler observes the loss (die hint + debounce), marks lost,
	// then after the recovery backoff (~10s) actuates a FRESH incarnation.
	// Allow generous headroom for the 5s debounce + 10s backoff + image run.
	newContainer := waitForNewContainer(t, dockerBin, agentID, firstContainer, 90*time.Second)
	if newContainer == firstContainer {
		t.Fatal("reconciler did not auto-restart a fresh incarnation after the out-of-band kill")
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	// RESTARTS climbs (operator-visible recovery score).
	deadline := time.Now().Add(30 * time.Second)
	var restarts int
	for time.Now().Before(deadline) {
		restarts = pollAgentStatus(t, cli, agentID).Restarts
		if restarts >= 1 {
			break
		}
		time.Sleep(time.Second)
	}
	if restarts < 1 {
		t.Fatalf("RESTARTS did not climb after auto-restart (restarts=%d)\n--- daemon log ---\n%s", restarts, h.tail(t))
	}

	cleanUpAgent(t, cli, agentID)
}

// TestRecovery_E2E_CrashLoopTripsBudgetToTerminal: repeatedly kill the
// container as it comes back; the crash budget (5 / 10 min) trips and the
// agent parks in terminal `crashed`, surfaced by list_agents. Timing-heavy
// (backoff grows 10→20→40→80→160) so it carries a multi-minute deadline —
// CI-only.
func TestRecovery_E2E_CrashLoopTripsBudgetToTerminal(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)
	ctx := context.Background()

	spawnCtx, spawnCancel := context.WithTimeout(ctx, 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "recover-crashloop",
		Template: "default",
	}, &spawnResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })
	waitForContainer(t, dockerBin, agentID, 30*time.Second)

	// Tight crash loop: kill every container that appears until the budget
	// trips. The reconciler's growing backoff means at most ~5 restarts
	// inside the 10-min window before it flips terminal.
	deadline := time.Now().Add(8 * time.Minute)
	tripped := false
	for time.Now().Before(deadline) {
		dockerKill(dockerBin, agentID)
		status := pollAgentStatus(t, cli, agentID)
		if status.Lifecycle == string(sextantproto.LifecycleCrashedState) {
			tripped = true
			if status.Restarts < 1 {
				t.Fatalf("crashed with RESTARTS=%d; expected the budget restarts to be counted", status.Restarts)
			}
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !tripped {
		st := pollAgentStatus(t, cli, agentID)
		t.Fatalf("crash budget never tripped to terminal crashed (lifecycle=%s restarts=%d)\n--- daemon log ---\n%s",
			st.Lifecycle, st.Restarts, h.tail(t))
	}

	// Surfaced in list_agents with the RESTARTS count.
	var listResp sextantproto.ListAgentsResponse
	listCtx, listCancel := context.WithTimeout(ctx, 15*time.Second)
	defer listCancel()
	if err := cli.RPC(listCtx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &listResp); err != nil {
		t.Fatalf("list_agents: %v", err)
	}
	found := false
	for _, a := range listResp.Agents {
		if a.UUID == agentID {
			found = true
			if a.Lifecycle != string(sextantproto.LifecycleCrashedState) {
				t.Errorf("list_agents lifecycle = %q, want crashed", a.Lifecycle)
			}
			if a.Restarts < 1 {
				t.Errorf("list_agents RESTARTS = %d, want >= 1", a.Restarts)
			}
		}
	}
	if !found {
		t.Fatal("crashed agent missing from list_agents")
	}

	cleanUpAgent(t, cli, agentID)
}

// TestRecovery_E2E_WedgedAgentLivenessRestart: a still-running but wedged
// agent (process alive, no heartbeats) is restarted by the liveness probe
// — the failure docker `die` never catches. We simulate the wedge by
// pausing the container so its sidecar stops heartbeating while the
// container stays "running" from docker's perspective is NOT enough
// (paused shows as paused); instead we stop the sidecar PROCESS inside the
// container so the heartbeat dies but the container keeps running.
func TestRecovery_E2E_WedgedAgentLivenessRestart(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)
	ctx := context.Background()

	spawnCtx, spawnCancel := context.WithTimeout(ctx, 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "recover-wedge",
		Template: "default",
	}, &spawnResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	firstContainer := waitForContainer(t, dockerBin, agentID, 30*time.Second)

	// Wedge: SIGSTOP the sidecar's node process inside the container so it
	// stops heartbeating while the container keeps running (docker `die`
	// never fires). The reconciler's liveness probe (3 stale probes / 10s
	// each) must catch it and route through the restart path.
	_ = exec.Command(dockerBin, "exec", firstContainer, //nolint:gosec // test-controlled args
		"/bin/sh", "-c", "kill -STOP 1 2>/dev/null || pkill -STOP node || true").Run()

	// Liveness needs the heartbeat to age past 3×10s plus a sweep; the
	// default sweep is 45s, so allow generous headroom.
	newContainer := waitForNewContainer(t, dockerBin, agentID, firstContainer, 3*time.Minute)
	if newContainer == firstContainer {
		t.Fatalf("liveness did not restart the wedged agent\n--- daemon log ---\n%s", h.tail(t))
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	cleanUpAgent(t, cli, agentID)
}

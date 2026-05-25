package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// requireDocker skips the M11 integration test when docker isn't on
// PATH. The leading prepend of $HOME/.orbstack/bin matches what
// images/sidecar/test.sh does so OrbStack-on-macOS gets picked up
// even when the test runs through `go test` (which doesn't load shell
// profile).
func requireDocker(t *testing.T) string {
	t.Helper()
	if home, err := os.UserHomeDir(); err == nil {
		orb := filepath.Join(home, ".orbstack", "bin")
		if st, statErr := os.Stat(orb); statErr == nil && st.IsDir() {
			t.Setenv("PATH", orb+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}
	p, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("docker not on PATH: %v (OrbStack not installed?)", err)
	}
	// Confirm the daemon is reachable; a bare `docker version` exits
	// non-zero if dockerd is down.
	cmd := exec.Command(p, "version", "--format", "{{.Server.Version}}") //nolint:gosec // test-controlled args
	if err := cmd.Run(); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	return p
}

// requireSidecarImage skips when the sextant-sidecar:latest image is
// not present locally. The M11 test would build it if missing, but
// the build adds ~3 minutes to the test — too slow for `make test`.
// CI exercises this via the dedicated sidecar-image job.
func requireSidecarImage(t *testing.T, dockerBin string) {
	t.Helper()
	cmd := exec.Command(dockerBin, "image", "inspect", "sextant-sidecar:latest") //nolint:gosec // test-controlled args
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Skipf("sextant-sidecar:latest not present locally (run `make sidecar-image`): %v", err)
	}
}

// TestM11SpawnFlowAcceptance is the milestone acceptance test:
//
//  1. Boot the daemon harness (NATS, ClickHouse, RPC, MCP all up).
//  2. Subscribe to `agents.*.lifecycle` BEFORE spawning so we don't
//     race the `started` envelope.
//  3. Call spawn_agent with name=assistant, template=default.
//  4. Assert: returned agent_id is a valid UUID.
//  5. Assert: container is running with the matching label.
//  6. Assert: list_agents returns the agent in lifecycle=running.
//  7. Assert: lifecycle.started envelope arrived on NATS.
//  8. Call kill_agent; assert the container is gone within ~20s.
//
// The test registers a force-removal cleanup BEFORE the assertion
// chain so a mid-test failure doesn't leak the container even when
// kill_agent doesn't run.
func TestM11SpawnFlowAcceptance(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	// Per-test label so multiple parallel test runs don't fight each
	// other's cleanup. The handler doesn't stamp this yet — we filter
	// the cleanup by the agent_uuid label instead (set unconditionally).

	// 2. Subscribe to lifecycle FIRST. Using a fresh subscriber against
	// the running NATS so we get the JetStream-buffered started event
	// even if the publish wins the race.
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	msgs, err := cli.Subscribe(subCtx, "agents.*.lifecycle", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	// 3. Spawn.
	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "assistant",
		Template: "default",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatal("spawn_agent returned zero UUID")
	}
	t.Logf("spawned agent uuid=%s", agentID)

	// Belt-and-suspenders cleanup. kill_agent at the end of the test
	// drives the happy-path teardown; this is the safety net for when
	// the test panics before reaching it.
	t.Cleanup(func() {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		ids := strings.Fields(strings.TrimSpace(string(out)))
		for _, id := range ids {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})

	// 5. Container running.
	if running := containersWithLabel(t, dockerBin, handlers.LabelAgentUUID, agentID.String()); len(running) == 0 {
		t.Fatalf("no container found with label %s=%s\n--- daemon log ---\n%s",
			handlers.LabelAgentUUID, agentID, h.tail(t))
	}

	// 6. list_agents shows it.
	listCtx, listCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer listCancel()
	var listResp sextantproto.ListAgentsResponse
	if err := cli.RPC(listCtx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &listResp); err != nil {
		t.Fatalf("list_agents: %v", err)
	}
	found := false
	for _, a := range listResp.Agents {
		if a.UUID == agentID {
			found = true
			if a.Lifecycle != "running" {
				t.Errorf("Lifecycle = %q, want running", a.Lifecycle)
			}
		}
	}
	if !found {
		t.Fatalf("agent %s missing from list_agents (got %d agents)", agentID, len(listResp.Agents))
	}

	// 7. lifecycle.started envelope arrived.
	if err := waitForLifecycleStarted(t, msgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v\n--- daemon log ---\n%s\n--- container logs ---\n%s",
			err, h.tail(t), containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// 8. kill_agent.
	killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer killCancel()
	var killResp sextantproto.KillAgentResponse
	if err := cli.RPC(killCtx, rpc.VerbKillAgent, sextantproto.KillAgentRequest{
		AgentID:      agentID,
		GraceSeconds: 5,
	}, &killResp); err != nil {
		t.Fatalf("kill_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if !killResp.OK {
		t.Errorf("kill_agent returned ok=false")
	}

	// Container is gone within 20s.
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 20*time.Second); err != nil {
		t.Fatalf("container still present after kill_agent: %v", err)
	}

	// Final guarantee: no daemon-spawned containers leak across this
	// test run.
	left := containersWithLabel(t, dockerBin, handlers.LabelAgentUUID, agentID.String())
	if len(left) != 0 {
		t.Fatalf("%d container(s) leaked after kill_agent: %v", len(left), left)
	}
}

// containersWithLabel returns the IDs of every container matching the
// given label. Includes stopped containers so we catch "started but
// crashed" cases.
func containersWithLabel(t *testing.T, dockerBin, key, value string) []string {
	t.Helper()
	out, err := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
		"--filter", "label="+key+"="+value,
		"--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return strings.Fields(strings.TrimSpace(string(out)))
}

// containerLogs returns the combined stderr+stdout of the first
// container matching the label. Used to surface a meaningful error
// when the lifecycle.started envelope never arrives.
func containerLogs(dockerBin, key, value string) string {
	out, err := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
		"--filter", "label="+key+"="+value,
		"--format", "{{.ID}}").Output()
	if err != nil {
		return fmt.Sprintf("docker ps: %v", err)
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		return "(no containers found)"
	}
	logsCmd := exec.Command(dockerBin, "logs", ids[0]) //nolint:gosec // test-controlled args
	logs, _ := logsCmd.CombinedOutput()
	return string(logs)
}

// waitForContainerGone polls `docker ps -a` until no container
// (running OR exited) matches the label, or the timeout elapses.
// Returns nil on success.
//
// We use `-a` here even though a `docker stop` immediately moves the
// container out of `docker ps` (running): with AutoRemove=true the
// daemon then asynchronously removes the container, and that's the
// state we actually want to wait for. Otherwise the next assertion
// (`containersWithLabel` with `-a`) races against AutoRemove and
// flakes.
func waitForContainerGone(dockerBin, key, value string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+key+"="+value,
			"--format", "{{.ID}}").Output()
		if strings.TrimSpace(string(out)) == "" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("timeout")
}

// waitForLifecycleStarted drains the subscription channel until a
// lifecycle envelope with transition=started for agentID arrives.
// Returns nil on success, error on timeout or msg.Err.
func waitForLifecycleStarted(t *testing.T, ch <-chan client.Message, agentID uuid.UUID, timeout time.Duration) error {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return errors.New("subscription closed before lifecycle.started arrived")
			}
			if msg.Err != nil {
				t.Logf("lifecycle decode err: %v", msg.Err)
				continue
			}
			if msg.Envelope.Kind != sextantproto.KindLifecycle {
				continue
			}
			var p sextantproto.LifecyclePayload
			if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
				t.Logf("lifecycle payload decode: %v", err)
				continue
			}
			if p.AgentUUID == agentID && p.Transition == sextantproto.LifecycleStarted {
				_ = msg.Ack()
				return nil
			}
		case <-deadline.C:
			return errors.New("timeout waiting for lifecycle.started")
		}
	}
}

// TestAgentCanEditWorkspaceFile proves that the SEXTANT_PERMISSION_MODE env
// var is correctly injected by the spawn handler and reaches the container.
// It spawns a mock-driver agent (permission_ceiling = "auto", which maps to
// "acceptEdits") and then uses `docker inspect` to confirm the container
// carries SEXTANT_PERMISSION_MODE=acceptEdits. The mock-driver template
// already has permission_ceiling = "auto" (set in writeMinimalInstall) so
// this test exercises the full spawn path without needing a real API call.
// See plans/issues/bug-sidecar-doesnt-set-permission-mode.md.
func TestAgentCanEditWorkspaceFile(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	defer lifeCancel()
	lifeMsgs, err := cli.Subscribe(lifeCtx, "agents.*.lifecycle", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe lifecycle: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	agentID := spawnMockAgent(t, h, cli, dockerBin, "perm-mode-")

	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v\n--- container logs ---\n%s",
			err, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// Retrieve the running container ID for this agent.
	out, err := exec.Command(dockerBin, "ps", //nolint:gosec // test-controlled args
		"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
		"--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		t.Fatalf("no running container for agent %s\n--- daemon log ---\n%s", agentID, h.tail(t))
	}
	containerID := ids[0]

	// Inspect the container's env to confirm SEXTANT_PERMISSION_MODE=acceptEdits.
	// docker inspect returns a JSON array; the Env field is []string of "K=V" form.
	inspectOut, err := exec.Command(dockerBin, "inspect", //nolint:gosec // test-controlled args
		"--format", "{{range .Config.Env}}{{.}}\n{{end}}",
		containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	envLines := strings.Split(strings.TrimSpace(string(inspectOut)), "\n")
	const wantEnv = "SEXTANT_PERMISSION_MODE=acceptEdits"
	var found bool
	for _, line := range envLines {
		if strings.TrimSpace(line) == wantEnv {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("container env missing %q; got env lines:\n%s", wantEnv, strings.Join(envLines, "\n"))
	}

	killAndConfirm(t, cli, h, dockerBin, agentID)
}

// TestNoOrphanContainersAfterTestSuite is a guardrail: after every
// other test in this package runs, no container we spawned remains.
// Scoping by `sextant.agent_uuid` alone matches *any* sextant agent
// — including an operator-owned long-running daemon (e.g. the
// standing `lead`) that predates the test suite. We narrow the filter
// to `sextant.test_run=<testRunLabel>` (set by startDaemonHarness via
// SEXTANT_TEST_RUN_LABEL → spawnRuntime.testRunLabel →
// handlers.SpawnDeps.TestRunLabel → LabelTestRun on every spawn) so
// the tripwire only catches containers this `go test` process
// created. Production containers never carry sextant.test_run.
func TestNoOrphanContainersAfterTestSuite(t *testing.T) {
	// Skip cleanly when docker isn't available — same gate the
	// acceptance test uses.
	dockerBin := requireDocker(t)
	out, err := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
		"--filter", "label="+handlers.LabelAgentUUID,
		"--filter", "label="+handlers.LabelTestRun+"="+testRunLabel(),
		"--format", "{{.Names}} ({{.ID}})").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	leftover := strings.TrimSpace(string(out))
	if leftover != "" {
		t.Errorf("orphan containers detected with %s=%s after suite:\n%s",
			handlers.LabelTestRun, testRunLabel(), leftover)
	}
}

// loadOperatorCreds is a tiny helper for the spawn acceptance test.
//
//nolint:unused // kept for future tests that exercise the auth path directly
func loadOperatorCreds(t *testing.T, h *daemonHarness) sextantd.OperatorCreds {
	t.Helper()
	creds, err := sextantd.ReadOperatorCreds(h.cfg.NATS.OperatorCreds)
	if err != nil {
		t.Fatalf("ReadOperatorCreds: %v", err)
	}
	return creds
}

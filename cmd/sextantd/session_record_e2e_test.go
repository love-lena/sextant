package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestSessionRecordE2E is the S0 acceptance e2e (RFC §5.10): the
// claude-projects bind-mount is gone, so the session record splits into a
// live frame-stream view, an on-demand authoritative .jsonl read, and a
// durable snapshot-on-stop. This drives all three against a real daemon +
// docker using the mock driver (the mock now writes the canonical
// in-container JSONL so the read/snapshot machinery is exercised without an
// Anthropic API call):
//
//  1. Prompt the agent and observe the LIVE view off the frame stream
//     (agents.<uuid>.frames) — no mount.
//  2. Confirm the agent's container has NO claude-projects bind-mount.
//  3. Fetch the authoritative in-container .jsonl via read_file
//     (the --raw path) and confirm it matches a direct docker-exec cat.
//  4. STOP the agent and read its durable snapshot from the agent data
//     dir, confirming it matches the in-container .jsonl.
//
// Requires a real Docker daemon + the sextant-sidecar:latest image; self-
// skips when either is unavailable.
func TestSessionRecordE2E(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	// Subscribe BEFORE spawning so DeliverAll captures every frame.
	frameCtx, frameCancel := context.WithCancel(context.Background())
	defer frameCancel()
	frameMsgs, err := cli.Subscribe(frameCtx, "agents.*.frames", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe frames: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	defer lifeCancel()
	lifeMsgs, err := cli.Subscribe(lifeCtx, "agents.*.lifecycle", client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe lifecycle: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}

	agentID := spawnMockAgent(t, h, cli, dockerBin, "s0-session-")
	if err := waitForLifecycleStarted(t, lifeMsgs, agentID, 30*time.Second); err != nil {
		t.Fatalf("lifecycle.started: %v", err)
	}
	containerID := waitForContainer(t, dockerBin, agentID, 30*time.Second)

	// (2) No claude-projects bind-mount on the container — the root fix.
	for _, m := range inspectMounts(t, dockerBin, containerID) {
		if m.Destination == "/home/agent/.claude/projects" {
			t.Errorf("container has a claude-projects bind-mount (%+v); S0 removed it", m)
		}
	}

	// (1) LIVE view: prompt, then collect frames off the stream until the
	// turn ends. This is exactly what `agents context` (default) reads.
	promptText := "record this turn"
	promptCtx, promptCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer promptCancel()
	var promptResp sextantproto.PromptAgentResponse
	if err := cli.RPC(promptCtx, rpc.VerbPromptAgent, sextantproto.PromptAgentRequest{
		AgentID: agentID, Content: promptText,
	}, &promptResp); err != nil {
		t.Fatalf("prompt_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	frames := collectFramesUntilTurnEnded(t, frameMsgs, lifeMsgs, agentID, 30*time.Second)
	assertAssistantTextContains(t, frames, "ack")

	// Resolve the session-log locators the way the CLI does.
	var statusResp sextantproto.GetAgentStatusResponse
	statusCtx, statusCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer statusCancel()
	if err := cli.RPC(statusCtx, rpc.VerbGetAgentStatus,
		sextantproto.GetAgentStatusRequest{AgentID: agentID}, &statusResp); err != nil {
		t.Fatalf("get_agent_status: %v", err)
	}
	sl := statusResp.Status.SessionLog
	if sl == nil {
		t.Fatal("get_agent_status returned no SessionLog")
	}
	if sl.ProjectsDir != "/home/agent/.claude/projects" {
		t.Errorf("SessionLog.ProjectsDir = %q, want in-container base", sl.ProjectsDir)
	}
	if sl.SessionID == "" || sl.ContainerJSONLPath == "" {
		t.Fatalf("SessionLog missing session id / jsonl path: %+v", sl)
	}

	// Wait for the mock's JSONL to land in-container (the write follows the
	// frames it just published).
	var inContainer string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out, statErr := dockerExecOut(dockerBin, containerID, "cat", sl.ContainerJSONLPath)
		if statErr == nil && strings.Contains(out, "ack") {
			inContainer = out
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if inContainer == "" {
		t.Fatalf("in-container JSONL %s never appeared\n--- container logs ---\n%s",
			sl.ContainerJSONLPath, containerLogs(dockerBin, handlers.LabelAgentUUID, agentID.String()))
	}

	// (3) The --raw read_file path returns the SAME bytes as the direct cat.
	var readResp sextantproto.ReadFileResponse
	readCtx, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer readCancel()
	if err := cli.RPC(readCtx, rpc.VerbReadFile,
		sextantproto.ReadFileRequest{AgentID: agentID, Path: sl.ContainerJSONLPath}, &readResp); err != nil {
		t.Fatalf("read_file (--raw): %v", err)
	}
	if string(readResp.Content) != inContainer {
		t.Errorf("read_file content != in-container cat:\n read_file=%q\n cat      =%q",
			string(readResp.Content), inContainer)
	}

	// (4) STOP the agent → the reconciler snapshots the JSONL into the data
	// dir. Then read the snapshot (the --backup path) and match it.
	killAndConfirm(t, cli, h, dockerBin, agentID)

	snapPath := filepath.Join(h.cfg.Paths.DataDir, "agents", agentID.String(), "session-snapshot.jsonl")
	if sl.SnapshotPath != snapPath {
		t.Errorf("SessionLog.SnapshotPath = %q, want %q", sl.SnapshotPath, snapPath)
	}
	var snap []byte
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, rerr := os.ReadFile(snapPath); rerr == nil && len(b) > 0 {
			snap = b
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot %s was never written\n--- daemon log ---\n%s", snapPath, h.tail(t))
	}
	if string(snap) != inContainer {
		t.Errorf("snapshot content != in-container JSONL:\n snapshot=%q\n in-ctr  =%q",
			string(snap), inContainer)
	}
}

// dockerExecOut runs a docker exec and returns combined output + error
// without failing the test (the caller polls).
func dockerExecOut(dockerBin, containerID string, args ...string) (string, error) {
	full := append([]string{"exec", "-u", "agent", "-w", "/workspace", containerID}, args...)
	out, err := exec.Command(dockerBin, full...).CombinedOutput() //nolint:gosec // test-controlled args
	return string(out), err
}

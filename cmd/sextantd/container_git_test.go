package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestSpawnedContainerHasGitConfig is the acceptance test for
// plans/issues/feat-container-git-config.md: a spawned container has
// /home/agent/.gitconfig in place with the expected sextant identity,
// so any `git commit` the agent runs picks up a meaningful author.
//
// Uses the worktree-enabled harness (so the M14 worktree branch of
// materializeWorkspace fires) — gitconfig is written for every spawn
// regardless, but the worktree spawn is the path that actually needs
// to commit.
func TestSpawnedContainerHasGitConfig(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h, _, _ := startDaemonHarnessWithWorktree(t)
	cli := rpcClient(t, h)

	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "committer-cfg",
		Template: "default",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatal("spawn_agent returned zero UUID")
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	containerID := waitForContainer(t, dockerBin, agentID, 30*time.Second)

	out := mustDockerExec(t, dockerBin, containerID,
		"git", "config", "--global", "--get", "user.name")
	want := "sextant committer-cfg"
	if strings.TrimSpace(out) != want {
		t.Errorf("git config user.name = %q, want %q", strings.TrimSpace(out), want)
	}
	emailOut := mustDockerExec(t, dockerBin, containerID,
		"git", "config", "--global", "--get", "user.email")
	wantEmailSuffix := agentID.String() + "@sextant.local"
	if strings.TrimSpace(emailOut) != wantEmailSuffix {
		t.Errorf("git config user.email = %q, want %q", strings.TrimSpace(emailOut), wantEmailSuffix)
	}

	cleanUpAgent(t, cli, agentID)
}

// TestSpawnedContainerCanGitCommit is the acceptance test for
// plans/issues/bug-worktree-gitdir-unreachable-in-container.md: an
// agent in a worktree container can `git status` (the gitdir pointer
// resolves), make a commit, and the commit lands on the host's
// worktree.
func TestSpawnedContainerCanGitCommit(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h, _, _ := startDaemonHarnessWithWorktree(t)
	cli := rpcClient(t, h)
	ctx := context.Background()

	// 1. Spawn the agent.
	spawnCtx, spawnCancel := context.WithTimeout(ctx, 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "committer-act",
		Template: "default",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatal("spawn_agent returned zero UUID")
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	containerID := waitForContainer(t, dockerBin, agentID, 30*time.Second)

	// 2. `git status` must exit zero — that's the primary fix for the
	// gitdir-pointer bug. Before the fix this errored with
	// "fatal: not a git repository: <host path>".
	mustDockerExec(t, dockerBin, containerID, "git", "status")

	// 3. Make a commit inside the container.
	mustDockerExecShell(t, dockerBin, containerID,
		"set -e; cd /workspace && echo hi > new && git add new && git commit -m container-side-test")

	// 4. The commit message is reachable from `git log` inside.
	logOut := mustDockerExec(t, dockerBin, containerID,
		"git", "log", "-1", "--format=%s")
	if strings.TrimSpace(logOut) != "container-side-test" {
		t.Errorf("in-container git log subject = %q, want %q",
			strings.TrimSpace(logOut), "container-side-test")
	}

	// 5. The commit is visible from the host worktree too.
	var listResp sextantproto.WorktreeListResponse
	if err := cli.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &listResp); err != nil {
		t.Fatalf("worktree_list: %v", err)
	}
	var hostWorktreePath string
	for _, w := range listResp.Worktrees {
		if w.OwningAgent == agentID {
			hostWorktreePath = w.Path
			break
		}
	}
	if hostWorktreePath == "" {
		t.Fatalf("no worktree owned by agent %s in worktree_list (got %d entries)",
			agentID, len(listResp.Worktrees))
	}
	hostLog := runCaptureDaemon(t, hostWorktreePath, "git", "log", "-1", "--format=%s")
	if hostLog != "container-side-test" {
		t.Errorf("host-side git log subject = %q, want %q", hostLog, "container-side-test")
	}

	cleanUpAgent(t, cli, agentID)
}

// waitForContainer polls until the spawned agent's container has a
// non-empty ID under the matching label. Returns the ID.
func waitForContainer(t *testing.T, dockerBin string, agentID uuid.UUID, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ids := containersWithLabel(t, dockerBin, handlers.LabelAgentUUID, agentID.String())
		if len(ids) > 0 {
			return ids[0]
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no container with label %s=%s within %s",
		handlers.LabelAgentUUID, agentID, timeout)
	return ""
}

// mustDockerExec runs `docker exec -u agent -w /workspace <id> <cmd...>`
// and fails the test if the command returns non-zero.
func mustDockerExec(t *testing.T, dockerBin, containerID string, args ...string) string {
	t.Helper()
	full := append([]string{"exec", "-u", "agent", "-w", "/workspace", containerID}, args...)
	cmd := exec.Command(dockerBin, full...) //nolint:gosec // test-controlled args
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// mustDockerExecShell wraps mustDockerExec for cases where we need a
// /bin/sh -c invocation (chained commands, redirections).
func mustDockerExecShell(t *testing.T, dockerBin, containerID, script string) string {
	t.Helper()
	return mustDockerExec(t, dockerBin, containerID, "/bin/sh", "-c", script)
}

// forceRemoveByAgent is the safety-net cleanup used in t.Cleanup: tear
// down any container carrying the matching agent_uuid label. Mirrors
// the pattern in TestM11SpawnFlowAcceptance.
func forceRemoveByAgent(dockerBin string, agentID uuid.UUID) {
	out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
		"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
		"--format", "{{.ID}}").Output()
	for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
		_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
	}
}

// cleanUpAgent calls kill_agent + waits for the container to be gone.
// Best-effort: t.Cleanup's forceRemoveByAgent is the safety net if the
// RPC path fails.
func cleanUpAgent(t *testing.T, cli *client.Client, agentID uuid.UUID) {
	t.Helper()
	killCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var killResp sextantproto.KillAgentResponse
	if err := cli.RPC(killCtx, rpc.VerbKillAgent,
		sextantproto.KillAgentRequest{AgentID: agentID, GraceSeconds: 5},
		&killResp); err != nil {
		t.Logf("kill_agent: %v (cleanup is best-effort)", err)
	}
}

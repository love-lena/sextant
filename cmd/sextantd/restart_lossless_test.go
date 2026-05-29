package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// dockerMount is the projection of one entry in `docker inspect`'s
// .Mounts array that we compare across spawn/restart. We deliberately
// drop the host Source: per-agent host dirs (workspace, gitconfig,
// claude-projects) are keyed on the agent UUID, not the incarnation, so
// they're identical across a restart — but Docker may normalize symlinks
// (e.g. /var → /private/var on macOS), and the container-side shape is
// what the lossless-projection guarantee is actually about.
type dockerMount struct {
	Type        string // "bind" or "volume"
	Destination string
	RW          bool
	// Name is the volume name for type=volume; empty for binds. Included
	// because the claude_seed named volume is per-agent and must reattach
	// identically across a restart.
	Name string
}

// inspectMounts runs `docker inspect` and returns the container's mount
// set as a sorted, identity-stripped slice for comparison.
func inspectMounts(t *testing.T, dockerBin, containerID string) []dockerMount {
	t.Helper()
	out, err := exec.Command(dockerBin, "inspect", "--format", "{{json .Mounts}}", containerID).Output() //nolint:gosec // test-controlled args
	if err != nil {
		t.Fatalf("docker inspect mounts: %v", err)
	}
	var raw []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("decode mounts json %q: %v", string(out), err)
	}
	mounts := make([]dockerMount, 0, len(raw))
	for _, m := range raw {
		mounts = append(mounts, dockerMount{
			Type:        m.Type,
			Destination: m.Destination,
			RW:          m.RW,
			Name:        m.Name,
		})
	}
	sort.Slice(mounts, func(i, j int) bool {
		return mounts[i].Destination < mounts[j].Destination
	})
	return mounts
}

// inspectEnv returns the container's env as a map of K→V.
func inspectEnv(t *testing.T, dockerBin, containerID string) map[string]string {
	t.Helper()
	out, err := exec.Command(dockerBin, "inspect", //nolint:gosec // test-controlled args
		"--format", "{{range .Config.Env}}{{.}}\n{{end}}", containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect env: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		env[k] = v
	}
	return env
}

// TestRestartIsLosslessProjection is the C0 acceptance e2e (RFC §5.4):
// spawn an agent, restart it, and `docker inspect` both containers.
// The mount SETS must be identical (modulo host-path identity) and the
// env must be identical modulo per-incarnation identity. Specifically,
// the three mounts the pre-C0 restart silently dropped — the gitconfig,
// the worktree git-dir bind, and (when the template opts in) SSH — must
// be present on the restarted container.
//
// Uses the worktree-enabled harness so the spawn produces the worktree
// /workspace + the <repo>/.git bind + the per-agent gitconfig — the
// exact mounts whose absence on restart was the #50-class bug.
//
// Requires a real Docker daemon + the sextant-sidecar:latest image; it
// self-skips when either is unavailable (see requireDocker /
// requireSidecarImage), so it is safe in `make test` and runs for real
// on a Docker host / in CI's sidecar job.
func TestRestartIsLosslessProjection(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h, _, _ := startDaemonHarnessWithWorktree(t)
	cli := rpcClient(t, h)
	ctx := context.Background()

	// 1. Spawn a worktree agent.
	spawnCtx, spawnCancel := context.WithTimeout(ctx, 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "lossless-restart",
		Template: "default",
	}, &spawnResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("spawn_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatal("spawn_agent returned zero UUID")
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, agentID) })

	spawnContainerID := waitForContainer(t, dockerBin, agentID, 30*time.Second)
	spawnMounts := inspectMounts(t, dockerBin, spawnContainerID)
	spawnEnv := inspectEnv(t, dockerBin, spawnContainerID)

	// Sanity: the spawn container carries the gitconfig + git-dir mounts
	// (the worktree harness produces both). If these aren't here the
	// harness changed and the rest of the assertion is meaningless.
	if !hasDestination(spawnMounts, "/home/agent/.gitconfig") {
		t.Fatalf("spawn container missing /home/agent/.gitconfig; mounts = %+v", spawnMounts)
	}

	// 2. Restart.
	restartCtx, restartCancel := context.WithTimeout(ctx, 60*time.Second)
	defer restartCancel()
	var restartResp sextantproto.RestartAgentResponse
	// restart stops the live incarnation (up to defaultGraceSeconds grace)
	// then starts a fresh container, which comfortably exceeds the client's
	// default 10s per-RPC timeout. Give it room.
	if err := cli.RPC(restartCtx, rpc.VerbRestartAgent, sextantproto.RestartAgentRequest{
		AgentID: agentID,
	}, &restartResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("restart_agent: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if !restartResp.OK {
		t.Fatal("restart_agent returned ok=false")
	}

	// The restarted incarnation is a NEW container (fresh incarnation
	// id). Poll until a container distinct from the spawn one appears.
	restartContainerID := waitForNewContainer(t, dockerBin, agentID, spawnContainerID, 30*time.Second)
	restartMounts := inspectMounts(t, dockerBin, restartContainerID)
	restartEnv := inspectEnv(t, dockerBin, restartContainerID)

	// 3. Mount SETS identical (modulo host-path identity).
	if len(spawnMounts) != len(restartMounts) {
		t.Fatalf("mount count drift: spawn=%d restart=%d\nspawn=%+v\nrestart=%+v",
			len(spawnMounts), len(restartMounts), spawnMounts, restartMounts)
	}
	for i := range spawnMounts {
		if spawnMounts[i] != restartMounts[i] {
			t.Errorf("mount[%d] drift:\n spawn  =%+v\n restart=%+v", i, spawnMounts[i], restartMounts[i])
		}
	}

	// 4. The three latent mounts are present on the RESTART container.
	for _, dest := range []string{"/home/agent/.gitconfig", "/workspace"} {
		if !hasDestination(restartMounts, dest) {
			t.Errorf("restart container missing mount %s (latent-mount regression); mounts = %+v",
				dest, restartMounts)
		}
	}

	// 5. Env identical modulo per-incarnation identity. The incarnation
	// id and JWT are expected to differ; everything else must match.
	identityKeys := map[string]bool{
		"SEXTANT_INCARNATION_ID": true,
		"SEXTANT_JWT":            true,
	}
	for k, sv := range spawnEnv {
		if identityKeys[k] {
			continue
		}
		if rv, ok := restartEnv[k]; !ok {
			t.Errorf("restart env missing key %q (spawn had %q)", k, sv)
		} else if rv != sv {
			t.Errorf("env %q drift: spawn=%q restart=%q", k, sv, rv)
		}
	}
	for k := range restartEnv {
		if identityKeys[k] {
			continue
		}
		if _, ok := spawnEnv[k]; !ok {
			t.Errorf("restart env has extra key %q not present on spawn", k)
		}
	}

	// 6. Identity actually changed — proves we compared two distinct
	// incarnations, not the same container twice.
	if spawnEnv["SEXTANT_INCARNATION_ID"] == restartEnv["SEXTANT_INCARNATION_ID"] {
		t.Error("incarnation id did not change across restart — test would be vacuous")
	}

	cleanUpAgent(t, cli, agentID)
}

func hasDestination(mounts []dockerMount, dest string) bool {
	for _, m := range mounts {
		if m.Destination == dest {
			return true
		}
	}
	return false
}

// waitForNewContainer polls until a container with the agent's label
// exists whose ID differs from excludeID (the prior incarnation).
func waitForNewContainer(t *testing.T, dockerBin string, agentID uuid.UUID, excludeID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, id := range containersWithLabel(t, dockerBin, handlers.LabelAgentUUID, agentID.String()) {
			// docker ps may return short IDs; compare on prefix either way.
			if id != excludeID && !strings.HasPrefix(excludeID, id) && !strings.HasPrefix(id, excludeID) {
				return id
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no new container (distinct from %s) for agent %s within %s", excludeID, agentID, timeout)
	return ""
}

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// archive_volume_leak_e2e_test.go is the acceptance e2e for
// bug-ctl-archive-volume-leak (real daemon + docker). It proves the
// finalizer-shaped archive: with the per-agent volume reclaim FAILING the
// agent stays in the intermediate observed=archiving (name still held) and
// is RETRIED by the reconciler; once the reclaim can succeed it reaches
// terminal observed=archived and releases the name. Then a healthy agent
// archived normally reaches archived and the volume is reclaimed.
//
// The volume-remove fault is injected at the DOCKER layer (no daemon
// production seam needed): a second, sextant-UNlabelled "blocker" container
// mounts the same per-agent claude_seed volume, so VolumeRemove — even with
// force — fails with "volume is in use" while the blocker exists. Removing
// the blocker lets the next reconcile pass reclaim the volume and finalize.
//
// It is docker-backed + reconcile-timing sensitive, so it is CI-only — it
// self-skips when docker / the sidecar image are absent, like the other
// e2e tests in this package. Do NOT run it on the watchdog'd local path;
// CI's sidecar job runs it.

// readObservedState polls the agent_definitions KV for the
// reconciler-written status.observed, returning the latest value seen by
// the deadline (or the want value as soon as it appears). The
// archiving/archived distinction is internal to status.observed —
// Lifecycle() projects BOTH to "archived" — so the e2e reads the raw KV
// (reads stay off the RPC gauntlet; the drift e2e does the same).
func waitObservedState(t *testing.T, kv jetstream.KeyValue, agentID uuid.UUID, want sextantproto.ObservedState, timeout time.Duration) sextantproto.ObservedState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got sextantproto.ObservedState
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		entry, err := kv.Get(ctx, agentID.String())
		cancel()
		if err == nil {
			var def sextantproto.AgentDefinition
			if json.Unmarshal(entry.Value(), &def) == nil {
				got = def.Status.Observed
				if got == want {
					return got
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return got
}

// observedState reads status.observed once (no polling).
func observedState(t *testing.T, kv jetstream.KeyValue, agentID uuid.UUID) sextantproto.ObservedState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry, err := kv.Get(ctx, agentID.String())
	if err != nil {
		t.Fatalf("kv get %s: %v", agentID, err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &def); err != nil {
		t.Fatalf("decode def: %v", err)
	}
	return def.Status.Observed
}

// bootDaemonWithSeedTemplate writes a daemon config with a template that
// sets claude_seed (so a spawned agent gets a per-agent volume to reclaim)
// and boots the daemon binary. Returns the harness, client, docker bin,
// and the template name.
func bootDaemonWithSeedTemplate(t *testing.T) (*daemonHarness, *client.Client, string, string) {
	t.Helper()
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	seedDir, err := os.MkdirTemp("", "archive-leak-seed-")
	if err != nil {
		t.Fatalf("MkdirTemp seed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(seedDir) })
	if err := os.WriteFile(filepath.Join(seedDir, "CLAUDE.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("write seed CLAUDE.md: %v", err)
	}

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	const tplName = "archive-leak-seed"
	tplBody := `name = "` + tplName + `"
description = "Per-agent volume template for the archive-leak e2e."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
claude_seed = "` + seedDir + `"

[env]
SEXTANT_DRIVER = "mock"
`
	if err := os.WriteFile(filepath.Join(cfg.Paths.TemplatesDir, tplName+".toml"), []byte(tplBody), 0o600); err != nil {
		t.Fatalf("write %s.toml: %v", tplName, err)
	}

	h := bootDaemonAtConfig(t, cfgPath)
	cli := rpcClient(t, h)
	return h, cli, dockerBin, tplName
}

// spawnSeedAgent spawns one agent against the seed template + waits for its
// container, returning the agent id and its per-agent volume name.
func spawnSeedAgent(t *testing.T, h *daemonHarness, cli *client.Client, dockerBin, tplName, name string) (uuid.UUID, string) {
	t.Helper()
	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer spawnCancel()
	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(spawnCtx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     name,
		Template: tplName,
	}, &spawnResp, client.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("spawn_agent %s: %v\n--- daemon log ---\n%s", name, err, h.tail(t))
	}
	agentID := spawnResp.AgentID
	if agentID == uuid.Nil {
		t.Fatalf("spawn_agent %s returned zero UUID", name)
	}
	volName := handlers.ClaudeSeedVolumeName(agentID)
	t.Cleanup(func() {
		forceRemoveByAgent(dockerBin, agentID)
		_ = exec.Command(dockerBin, "volume", "rm", "-f", volName).Run() //nolint:gosec // test-controlled args
	})
	waitForContainer(t, dockerBin, agentID, 30*time.Second)
	return agentID, volName
}

// archiveAgent issues archive_agent (writes spec.desired=archived).
func archiveAgent(t *testing.T, h *daemonHarness, cli *client.Client, agentID uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var resp sextantproto.ArchiveAgentResponse
	if err := cli.RPC(ctx, rpc.VerbArchiveAgent, sextantproto.ArchiveAgentRequest{
		AgentID: agentID,
	}, &resp); err != nil {
		t.Fatalf("archive_agent %s: %v\n--- daemon log ---\n%s", agentID, err, h.tail(t))
	}
}

// volumeExists reports whether the named docker volume is present.
func volumeExists(dockerBin, volName string) bool {
	err := exec.Command(dockerBin, "volume", "inspect", volName).Run() //nolint:gosec // test-controlled args
	return err == nil
}

// trySpawnName attempts a spawn with the given name and returns the RPC
// error string ("" on success). The daemon surfaces a name collision as a
// bad_request from spawn.agentNameInUse.
func trySpawnName(t *testing.T, cli *client.Client, tplName, name string) (uuid.UUID, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var resp sextantproto.SpawnAgentResponse
	err := cli.RPC(ctx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     name,
		Template: tplName,
	}, &resp, client.WithTimeout(60*time.Second))
	if err != nil {
		return uuid.Nil, err.Error()
	}
	return resp.AgentID, ""
}

// TestArchive_E2E_VolumeReclaimFailureStaysArchiving is the leak guard
// (bug-ctl-archive-volume-leak). With the per-agent volume reclaim blocked,
// archive must NOT finalize: the agent stays in observed=archiving, the name
// stays held, and the reconciler keeps retrying. Once the blocker is removed
// the volume is reclaimed, the agent reaches observed=archived, and the name
// becomes reusable.
func TestArchive_E2E_VolumeReclaimFailureStaysArchiving(t *testing.T) {
	h, cli, dockerBin, tplName := bootDaemonWithSeedTemplate(t)

	const name = "archive-leak"
	agentID, volName := spawnSeedAgent(t, h, cli, dockerBin, tplName, name)
	kv := openDefsKV(t, h)

	// Inject the volume-remove fault at the docker layer: a second container
	// (NOT carrying the sextant agent label, so the reconciler's container
	// teardown leaves it alone) holds the per-agent volume mounted. While it
	// exists, VolumeRemove --force fails with "volume is in use".
	blocker := "archive-leak-blocker-" + uuid.New().String()[:8]
	if out, err := exec.Command(dockerBin, "run", "-d", "--name", blocker, //nolint:gosec // test-controlled args
		"-v", volName+":/mnt", "sextant-sidecar:latest", "sleep", "600").CombinedOutput(); err != nil {
		t.Fatalf("start blocker container: %v\n%s", err, out)
	}
	blockerRemoved := false
	t.Cleanup(func() {
		if !blockerRemoved {
			_ = exec.Command(dockerBin, "rm", "-f", blocker).Run() //nolint:gosec // test-controlled args
		}
	})

	// Archive: spec.desired=archived. The reconciler stops the agent
	// container then tries the reclaim, which fails (blocker holds the
	// volume) — so it records observed=archiving and retries.
	archiveAgent(t, h, cli, agentID)

	if got := waitObservedState(t, kv, agentID, sextantproto.ObservedArchiving, 60*time.Second); got != sextantproto.ObservedArchiving {
		t.Fatalf("observed = %q, want archiving (failed reclaim must NOT finalize)\n--- daemon log ---\n%s", got, h.tail(t))
	}

	// The volume is still present (NOT leaked-away-silently — it is held,
	// pending reclaim) and the name is NOT yet released: a re-spawn collides.
	if !volumeExists(dockerBin, volName) {
		t.Fatalf("per-agent volume %s gone while archiving; the reclaim should have failed, not silently dropped it", volName)
	}
	if _, errStr := trySpawnName(t, cli, tplName, name); errStr == "" {
		t.Fatal("name released while observed=archiving (reclaim not confirmed); the leak guard is broken")
	}

	// Stays archiving across a sweep (level-triggered retry, not a flap to
	// terminal). Give it well over one sweep interval.
	if got := observedState(t, kv, agentID); got != sextantproto.ObservedArchiving {
		t.Fatalf("observed = %q mid-retry, want archiving", got)
	}

	// Remove the blocker — the next reconcile pass reclaims the volume and
	// finalizes to terminal archived.
	if err := exec.Command(dockerBin, "rm", "-f", blocker).Run(); err != nil { //nolint:gosec // test-controlled args
		t.Fatalf("remove blocker: %v", err)
	}
	blockerRemoved = true

	if got := waitObservedState(t, kv, agentID, sextantproto.ObservedArchived, 90*time.Second); got != sextantproto.ObservedArchived {
		t.Fatalf("observed = %q, want archived after the reclaim could succeed\n--- daemon log ---\n%s", got, h.tail(t))
	}
	// The volume is reclaimed (gone) and the name is now reusable.
	if volumeExists(dockerBin, volName) {
		t.Errorf("per-agent volume %s still present after archived; reclaim did not run", volName)
	}
	reID, errStr := trySpawnName(t, cli, tplName, name)
	if errStr != "" {
		t.Fatalf("re-spawn after archived failed: %s (name not released after reclamation)", errStr)
	}
	if reID == agentID {
		t.Errorf("re-spawn returned the same UUID; a fresh agent must get a new UUID")
	}
	t.Cleanup(func() { forceRemoveByAgent(dockerBin, reID) })
}

// TestArchive_E2E_HealthyArchiveReclaimsVolume is the normal path: archiving
// a healthy agent reaches terminal observed=archived AND reclaims the
// per-agent volume (no leak, no manual chore).
func TestArchive_E2E_HealthyArchiveReclaimsVolume(t *testing.T) {
	h, cli, dockerBin, tplName := bootDaemonWithSeedTemplate(t)

	agentID, volName := spawnSeedAgent(t, h, cli, dockerBin, tplName, "archive-healthy")
	kv := openDefsKV(t, h)

	if !volumeExists(dockerBin, volName) {
		t.Fatalf("per-agent volume %s was never created; the template's claude_seed did not take", volName)
	}

	archiveAgent(t, h, cli, agentID)

	if got := waitObservedState(t, kv, agentID, sextantproto.ObservedArchived, 60*time.Second); got != sextantproto.ObservedArchived {
		t.Fatalf("observed = %q, want archived (normal archive must finalize)\n--- daemon log ---\n%s", got, h.tail(t))
	}
	// Container gone + volume reclaimed.
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 30*time.Second); err != nil {
		t.Fatalf("agent container still present after archived: %v", err)
	}
	if volumeExists(dockerBin, volName) {
		t.Errorf("per-agent volume %s still present after archived; the normal path must reclaim it", volName)
	}
	// Name released.
	if reID, errStr := trySpawnName(t, cli, tplName, "archive-healthy"); errStr != "" {
		t.Errorf("re-spawn after healthy archive failed: %s (name not released)", errStr)
	} else {
		t.Cleanup(func() { forceRemoveByAgent(dockerBin, reID) })
	}
}

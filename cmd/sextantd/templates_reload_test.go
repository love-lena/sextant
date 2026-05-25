package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/templates"
)

// TestTemplatesReloadCLI proves the no-restart reload contract:
//
//  1. Daemon up; the harness's writeMinimalInstall seeds 2 templates
//     (default + mock-driver).
//  2. Write a new lead.toml to the templates dir.
//  3. Publish on sextant.control.templates_reload (same wire shape
//     `sextant templates reload` uses).
//  4. Assert response.Count = 3.
//  5. Assert the new template is queryable from KV — proves the spawn
//     handler can resolve it without a daemon restart.
//  6. Re-run the reload — idempotency: count stays at 3.
//
// This is the lighter half of the acceptance pair; the Docker-gated
// half (TestTemplatesReloadCLIAcceptance) drives the spawn flow against
// the reloaded template.
func TestTemplatesReloadCLI(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Write a new template — same shape `sextant init` would write.
	leadPath := filepath.Join(h.cfg.Paths.TemplatesDir, "lead.toml")
	leadTOML := `name = "lead"
description = "Lead-tier template added at runtime."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "control.spawn"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
`
	if err := os.WriteFile(leadPath, []byte(leadTOML), 0o600); err != nil {
		t.Fatalf("write lead.toml: %v", err)
	}

	reqRaw, err := json.Marshal(sextantd.TemplatesReloadRequest{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	reply, err := cli.Conn().RequestWithContext(ctx,
		sextantd.ControlTemplatesReloadSubject, reqRaw)
	if err != nil {
		t.Fatalf("RequestWithContext: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	var resp sextantd.TemplatesReloadResponse
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("decode response: %v (raw=%q)", err, reply.Data)
	}
	if resp.Error != "" {
		t.Fatalf("reload returned error: %s", resp.Error)
	}
	// writeMinimalInstall seeds 2 templates (default + mock-driver) and
	// we've added one (lead). The exact count is 3.
	if resp.Count != 3 {
		t.Fatalf("Count = %d, want 3", resp.Count)
	}

	// Read the new template back from KV — verifies the daemon pushed
	// it without a restart.
	js, err := jetstream.New(cli.Conn())
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	kv, err := js.KeyValue(ctx, templates.Bucket)
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	tpl, err := templates.LoadFromKV(ctx, kv, "lead")
	if err != nil {
		t.Fatalf("LoadFromKV lead: %v", err)
	}
	if tpl.Name != "lead" || tpl.Image != "sextant-sidecar:latest" {
		t.Errorf("tpl = %+v", tpl)
	}

	// Idempotency: a second reload returns the same count.
	reply2, err := cli.Conn().RequestWithContext(ctx,
		sextantd.ControlTemplatesReloadSubject, reqRaw)
	if err != nil {
		t.Fatalf("re-request: %v", err)
	}
	var resp2 sextantd.TemplatesReloadResponse
	if err := json.Unmarshal(reply2.Data, &resp2); err != nil {
		t.Fatalf("decode re-response: %v", err)
	}
	if resp2.Count != 3 {
		t.Errorf("re-reload Count = %d, want 3", resp2.Count)
	}
}

// TestTemplatesReloadCLISurfacesError proves a broken template surfaces
// in the daemon's reply Error field — without it the operator would see
// a cryptic "synced 0" and not know the file is malformed.
func TestTemplatesReloadCLISurfacesError(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bogusPath := filepath.Join(h.cfg.Paths.TemplatesDir, "broken.toml")
	// Missing required `image` field — Validate rejects it.
	if err := os.WriteFile(bogusPath, []byte(`name = "broken"`+"\n"+`permissions = ["read.agents"]`+"\n"), 0o600); err != nil {
		t.Fatalf("write broken.toml: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(bogusPath) })

	reqRaw, _ := json.Marshal(sextantd.TemplatesReloadRequest{})
	reply, err := cli.Conn().RequestWithContext(ctx,
		sextantd.ControlTemplatesReloadSubject, reqRaw)
	if err != nil {
		t.Fatalf("RequestWithContext: %v", err)
	}
	var resp sextantd.TemplatesReloadResponse
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == "" {
		t.Fatalf("expected non-empty Error in response (got Count=%d)", resp.Count)
	}
	if !strings.Contains(resp.Error, "image") {
		t.Errorf("Error = %q, want substring \"image\"", resp.Error)
	}
}

// TestTemplatesReloadCLIAcceptance is the Docker-gated half of the
// acceptance: reload the templates dir at runtime, then spawn an agent
// using the freshly-added template without a daemon restart. Mirrors
// `TestM11SpawnFlowAcceptance` but against a runtime-installed
// template, which is the load-bearing contract in
// plans/issues/feat-templates-reload-cli-verb.md.
func TestTemplatesReloadCLIAcceptance(t *testing.T) {
	dockerBin := requireDocker(t)
	requireSidecarImage(t, dockerBin)

	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	leadPath := filepath.Join(h.cfg.Paths.TemplatesDir, "lead.toml")
	leadTOML := `name = "lead"
description = "Lead-tier template added at runtime."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "control.spawn"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
`
	if err := os.WriteFile(leadPath, []byte(leadTOML), 0o600); err != nil {
		t.Fatalf("write lead.toml: %v", err)
	}

	reqRaw, _ := json.Marshal(sextantd.TemplatesReloadRequest{})
	reply, err := cli.Conn().RequestWithContext(ctx,
		sextantd.ControlTemplatesReloadSubject, reqRaw)
	if err != nil {
		t.Fatalf("reload request: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	var resp sextantd.TemplatesReloadResponse
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "" || resp.Count != 3 {
		t.Fatalf("reload: %+v", resp)
	}

	var spawnResp sextantproto.SpawnAgentResponse
	if err := cli.RPC(ctx, rpc.VerbSpawnAgent, sextantproto.SpawnAgentRequest{
		Name:     "reload-lead",
		Template: "lead",
	}, &spawnResp); err != nil {
		t.Fatalf("spawn_agent against reloaded template: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	agentID := spawnResp.AgentID

	// Belt-and-suspenders cleanup so a mid-test panic doesn't leak.
	t.Cleanup(func() {
		out, _ := exec.Command(dockerBin, "ps", "-a", //nolint:gosec // test-controlled args
			"--filter", "label="+handlers.LabelAgentUUID+"="+agentID.String(),
			"--format", "{{.ID}}").Output()
		for _, id := range strings.Fields(strings.TrimSpace(string(out))) {
			_ = exec.Command(dockerBin, "rm", "-f", id).Run() //nolint:gosec // test-controlled args
		}
	})

	if running := containersWithLabel(t, dockerBin, handlers.LabelAgentUUID, agentID.String()); len(running) == 0 {
		t.Fatal("no container present after spawn against reloaded template")
	}

	var killResp sextantproto.KillAgentResponse
	if err := cli.RPC(ctx, rpc.VerbKillAgent, sextantproto.KillAgentRequest{
		AgentID: agentID, GraceSeconds: 5,
	}, &killResp); err != nil {
		t.Fatalf("kill_agent: %v", err)
	}
	if err := waitForContainerGone(dockerBin, handlers.LabelAgentUUID, agentID.String(), 20*time.Second); err != nil {
		t.Fatalf("container still present after kill: %v", err)
	}
}

package templates

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/natsboot"
)

// natsServerPath skips when nats-server is not on PATH (CI without
// nats-server installed). Matches the same shape pkg/natsboot uses.
func natsServerPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
	return p
}

const sampleDefaultTOML = `
name = "default"
description = "Minimal default."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
`

const sampleHeavyTOML = `
name = "heavy"
description = "Wide-permission agent."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "read.history", "control.spawn", "control.kill"]
env = { MY_VAR = "true" }
mounts = ["worktree", "secrets"]
initial_prompt = "Welcome."
model = "claude-opus-4-7[1m]"
`

func TestLoadFromFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.toml")
	if err := os.WriteFile(path, []byte(sampleDefaultTOML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tpl, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if tpl.Name != "default" {
		t.Errorf("Name = %q, want default", tpl.Name)
	}
	if tpl.Image != "sextant-sidecar:latest" {
		t.Errorf("Image = %q", tpl.Image)
	}
	if len(tpl.Permissions) != 2 {
		t.Errorf("Permissions = %v", tpl.Permissions)
	}
}

func TestTemplateValidationRejectsMissingClaudeSeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.toml")
	body := `name = "seed"
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
claude_seed = "/nonexistent/sextant-claude-seed-` + filepath.Base(dir) + `"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected validation error for missing claude_seed dir")
	}
	if !strings.Contains(err.Error(), "claude_seed") {
		t.Errorf("err = %v, want substring \"claude_seed\"", err)
	}
}

func TestTemplateValidationAcceptsExistingClaudeSeed(t *testing.T) {
	// Sanity: an existing directory satisfies validation. Guards against
	// the validation being so strict it rejects the happy path.
	seedDir := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "seed-ok.toml")
	body := `name = "seed-ok"
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
claude_seed = "` + seedDir + `"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
}

// TestResolveClaudeSeedModeDefaults pins the
// bug-claude-seed-readonly-breaks-session-persistence fix: a template
// that sets claude_seed without claude_seed_mode must resolve to
// "copy-on-spawn" so the SDK can write its session journal. The
// previous behavior (readonly bind) is preserved as an explicit opt-in.
func TestResolveClaudeSeedModeDefaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		tpl  Template
		want string
	}{
		{
			name: "empty seed → empty mode",
			tpl:  Template{},
			want: "",
		},
		{
			name: "seed set, mode unset → copy-on-spawn",
			tpl:  Template{ClaudeSeed: "/some/path"},
			want: ClaudeSeedModeCopyOnSpawn,
		},
		{
			name: "seed set, mode copy-on-spawn → copy-on-spawn",
			tpl:  Template{ClaudeSeed: "/some/path", ClaudeSeedMode: ClaudeSeedModeCopyOnSpawn},
			want: ClaudeSeedModeCopyOnSpawn,
		},
		{
			name: "seed set, mode readonly-bind → readonly-bind",
			tpl:  Template{ClaudeSeed: "/some/path", ClaudeSeedMode: ClaudeSeedModeReadonly},
			want: ClaudeSeedModeReadonly,
		},
		{
			name: "empty seed but mode set → empty (mode is meaningless)",
			tpl:  Template{ClaudeSeedMode: ClaudeSeedModeReadonly},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tpl.ResolveClaudeSeedMode(); got != tc.want {
				t.Errorf("ResolveClaudeSeedMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTemplateValidationRejectsUnknownClaudeSeedMode confirms invalid
// claude_seed_mode values fail validation at load time so an operator
// typo surfaces loudly instead of falling through to the default.
func TestTemplateValidationRejectsUnknownClaudeSeedMode(t *testing.T) {
	seedDir := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-mode.toml")
	body := `name = "bad-mode"
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
claude_seed = "` + seedDir + `"
claude_seed_mode = "garbage-not-a-mode"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected validation error for unknown claude_seed_mode")
	}
	if !strings.Contains(err.Error(), "claude_seed_mode") {
		t.Errorf("err = %v, want substring \"claude_seed_mode\"", err)
	}
}

// TestTemplateValidationAcceptsReadonlyBindMode confirms the legacy
// "readonly-bind" mode is still accepted (it's the opt-in for agents
// that genuinely don't need the SDK to persist state).
func TestTemplateValidationAcceptsReadonlyBindMode(t *testing.T) {
	seedDir := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "ro.toml")
	body := `name = "ro"
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
claude_seed = "` + seedDir + `"
claude_seed_mode = "readonly-bind"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tpl, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if got := tpl.ResolveClaudeSeedMode(); got != ClaudeSeedModeReadonly {
		t.Errorf("ResolveClaudeSeedMode() = %q, want %q", got, ClaudeSeedModeReadonly)
	}
}

// TestMountsAcceptsSSH pins the feat-container-ssh-passthrough fix:
// templates may declare `mounts = ["worktree", "ssh"]` to opt into the
// ~/.ssh bind mount the spawn handler attaches read-only at
// /home/agent/.ssh. The ssh class is opt-in (no default template lists
// it); validation must accept it explicitly so a typo'd value (e.g.
// "shh") still errors. See plans/issues/feat-container-ssh-passthrough.md.
func TestMountsAcceptsSSH(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "with-ssh.toml")
	body := `name = "with-ssh"
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
mounts = ["worktree", "ssh"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tpl, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if len(tpl.Mounts) != 2 || tpl.Mounts[1] != "ssh" {
		t.Errorf("Mounts = %v, want [worktree ssh]", tpl.Mounts)
	}
}

// TestMountsRejectsUnknownClass guards the mount-class allowlist —
// arbitrary strings must error at validation so a typo doesn't silently
// produce an agent missing the intended mount. The error message must
// name the offending value so the operator can spot it in the TOML.
func TestMountsRejectsUnknownClass(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-mount.toml")
	body := `name = "bad-mount"
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
mounts = ["worktree", "shh"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected validation error for unknown mount class")
	}
	if !strings.Contains(err.Error(), "shh") {
		t.Errorf("err = %v, want substring \"shh\"", err)
	}
}

func TestLoadFromFileMissingPermissionsFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte(`name = "bad"`+"\n"+`image = "img"`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadFromFile(path); err == nil {
		t.Fatal("expected validation error for missing permissions")
	}
}

func TestLoadDirSortsLexically(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.toml"), []byte(sampleDefaultTOML), 0o600); err != nil {
		t.Fatalf("write default: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy.toml"), []byte(sampleHeavyTOML), 0o600); err != nil {
		t.Fatalf("write heavy: %v", err)
	}
	tpls, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(tpls) != 2 {
		t.Fatalf("len = %d, want 2", len(tpls))
	}
	if tpls[0].Name != "default" || tpls[1].Name != "heavy" {
		t.Errorf("order = %v", []string{tpls[0].Name, tpls[1].Name})
	}
}

func TestSyncDirToKVRoundtrip(t *testing.T) {
	bin := natsServerPath(t)

	dir := t.TempDir()
	natsDir := filepath.Join(dir, "nats")
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0o700); err != nil {
		t.Fatalf("mkdir tmpl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "default.toml"), []byte(sampleDefaultTOML), 0o600); err != nil {
		t.Fatalf("write default: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "heavy.toml"), []byte(sampleHeavyTOML), 0o600); err != nil {
		t.Fatalf("write heavy: %v", err)
	}

	cfg := natsboot.DefaultConfig(natsDir)
	cfg.NATSBinary = bin
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srv, err := natsboot.Start(ctx, cfg)
	if err != nil {
		t.Fatalf("nats Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()
	if err := natsboot.Bootstrap(ctx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	kv, err := js.KeyValue(ctx, Bucket)
	if err != nil {
		t.Fatalf("kv: %v", err)
	}

	synced, err := SyncDirToKV(ctx, kv, tmplDir)
	if err != nil {
		t.Fatalf("SyncDirToKV: %v", err)
	}
	if len(synced) != 2 {
		t.Errorf("synced = %d, want 2", len(synced))
	}

	tpl, err := LoadFromKV(ctx, kv, "default")
	if err != nil {
		t.Fatalf("LoadFromKV default: %v", err)
	}
	if tpl.Image != "sextant-sidecar:latest" {
		t.Errorf("Image = %q", tpl.Image)
	}

	// Re-sync (idempotency).
	if _, err := SyncDirToKV(ctx, kv, tmplDir); err != nil {
		t.Fatalf("SyncDirToKV (second call): %v", err)
	}

	// Missing template = ErrNotFound wrapped.
	if _, err := LoadFromKV(ctx, kv, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSyncDirToKVEmptyDirIsError(t *testing.T) {
	dir := t.TempDir()
	_, err := SyncDirToKV(context.Background(), &nopKV{}, dir)
	if err == nil {
		t.Fatal("expected error for empty templates dir")
	}
}

// nopKV is a minimal KV implementation used by tests that should fail
// before ever touching the bucket.
type nopKV struct{}

func (nopKV) Put(_ context.Context, _ string, _ []byte) (uint64, error) {
	return 0, errors.New("nopKV: not implemented")
}

func (nopKV) Get(_ context.Context, _ string) (jetstream.KeyValueEntry, error) {
	return nil, jetstream.ErrKeyNotFound
}

func (nopKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	return nil, errors.New("nopKV: not implemented")
}

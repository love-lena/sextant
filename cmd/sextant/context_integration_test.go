package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ctxBin is the sextant binary built once for the context CLI integration test.
var ctxBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sxctx")
	if err != nil {
		panic(err)
	}
	ctxBin = filepath.Join(dir, "sextant")
	if out, err := exec.Command("go", "build", "-o", ctxBin, ".").CombinedOutput(); err != nil {
		panic("build sextant: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func runCtx(t *testing.T, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(ctxBin, args...)
	cmd.Env = append(
		os.Environ(),
		"SEXTANT_HOME="+home,
		"SEXTANT_STORE="+filepath.Join(home, "store"), // isolated; no bus.json here
	)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err == nil {
		return buf.String(), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return buf.String(), ee.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", -1
}

// TestContextLifecycle drives the whole `sextant context` surface through the
// built binary: add → current → list → use → delete, and the clear-active and
// creds-copy/purge behaviors. Subprocess so dispatch and launch-path bugs show.
func TestContextLifecycle(t *testing.T) {
	home := t.TempDir()
	dummy := filepath.Join(home, "dummy.creds")
	if err := os.WriteFile(dummy, []byte("CREDS-BLOB"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, code := runCtx(t, home, "context", "list"); code != 0 || !strings.Contains(out, "no contexts") {
		t.Fatalf("list (empty): code=%d out=%q", code, out)
	}

	// add alice → becomes active (first context)
	if out, code := runCtx(t, home, "context", "add", "alice", "--creds", dummy, "--url", "nats://x"); code != 0 || !strings.Contains(out, "now active") {
		t.Fatalf("add alice: code=%d out=%q", code, out)
	}
	if out, code := runCtx(t, home, "context", "current"); code != 0 || strings.TrimSpace(out) != "alice" {
		t.Fatalf("current after add alice: code=%d out=%q", code, out)
	}

	// add bob → saved but NOT active (alice already active)
	if out, code := runCtx(t, home, "context", "add", "bob", "--creds", dummy, "--url", "nats://y"); code != 0 || strings.Contains(out, "now active") {
		t.Fatalf("add bob (should not auto-activate): code=%d out=%q", code, out)
	}

	// creds copied into the config creds dir (self-contained, not referencing the source)
	if _, err := os.Stat(filepath.Join(home, "creds", "bob.creds")); err != nil {
		t.Fatalf("bob creds not copied into config: %v", err)
	}

	out, code := runCtx(t, home, "context", "list")
	if code != 0 || !strings.Contains(out, "alice") || !strings.Contains(out, "bob") {
		t.Fatalf("list: code=%d out=%q", code, out)
	}

	// switch active to bob
	if _, code := runCtx(t, home, "context", "use", "bob"); code != 0 {
		t.Fatalf("use bob: code=%d", code)
	}
	if out, _ := runCtx(t, home, "context", "current"); strings.TrimSpace(out) != "bob" {
		t.Fatalf("current after use bob: %q", out)
	}

	// delete the active context (bob) with --purge → record + creds gone, active cleared
	if _, code := runCtx(t, home, "context", "delete", "bob", "--purge"); code != 0 {
		t.Fatalf("delete bob: code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "creds", "bob.creds")); !os.IsNotExist(err) {
		t.Fatalf("--purge did not remove bob creds: %v", err)
	}
	if out, code := runCtx(t, home, "context", "current"); code == 0 {
		t.Fatalf("current should fail after deleting the active context: out=%q", out)
	}

	// re-adding an existing context is refused without --force (no silent clobber)
	if out, code := runCtx(t, home, "context", "add", "alice", "--creds", dummy, "--url", "nats://z"); code == 0 {
		t.Fatalf("re-add alice without --force should fail: out=%q", out)
	}
	// ...and allowed with --force
	if out, code := runCtx(t, home, "context", "add", "alice", "--creds", dummy, "--url", "nats://z", "--force"); code != 0 {
		t.Fatalf("re-add alice with --force should succeed: code=%d out=%q", code, out)
	}

	// using a missing context errors
	if out, code := runCtx(t, home, "context", "use", "ghost"); code == 0 {
		t.Fatalf("use ghost should fail: out=%q", out)
	}
}

package main

import (
	"encoding/json"
	"errors"
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
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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

// ctxRow mirrors `context list --json`'s row shape (the fields this test asserts).
type ctxRow struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

func ctxRowByName(t *testing.T, home, name string) ctxRow {
	t.Helper()
	out, code := runCtx(t, home, "context", "list", "--json")
	if code != 0 {
		t.Fatalf("list --json: code=%d out=%q", code, out)
	}
	var rows []ctxRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse list --json %q: %v", out, err)
	}
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("context %q not in list --json: %q", name, out)
	return ctxRow{}
}

// TestContextAddPreservesIDAndKindOnReadd is TASK-62's regression lock: the
// v0.5.1 restart incident stranded the whole crew because a discovery-mode
// `context add --force` re-add (which does not repeat --id/--kind) EMPTIED the
// id and omitted the kind, so `context use` then refused the context for
// kind=="" — each named agent had to hand-edit its context json to recover.
//
// A named agent context is created with `--kind agent` carrying a bus identity,
// and a bare `--force` re-add must PRESERVE both id and kind=agent so the
// context still satisfies the (unchanged) context_use agent-kind guard with no
// manual edit. We drive the real binary so the flag-parsing + write path that
// actually stranded the crew is the thing under test.
func TestContextAddPreservesIDAndKindOnReadd(t *testing.T) {
	home := t.TempDir()
	dummy := filepath.Join(home, "agent.creds")
	if err := os.WriteFile(dummy, []byte("CREDS-BLOB"), 0o600); err != nil {
		t.Fatal(err)
	}

	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	// Create a named agent context carrying a bus identity, exactly as a named
	// crew agent is enrolled (--kind agent so context_use will attach to it).
	if out, code := runCtx(t, home, "context", "add", "sirius",
		"--creds", dummy, "--url", "nats://crew", "--kind", "agent", "--id", id); code != 0 {
		t.Fatalf("add sirius: code=%d out=%q", code, out)
	}
	if r := ctxRowByName(t, home, "sirius"); r.ID != id || r.Kind != "agent" {
		t.Fatalf("after add: id=%q kind=%q, want id=%q kind=agent", r.ID, r.Kind, id)
	}

	// The recovery re-add: --force, NO --id/--kind (the operator does not repeat
	// them in discovery mode). The id and kind must SURVIVE — this is the exact
	// regression that emptied them and stranded the crew.
	if out, code := runCtx(t, home, "context", "add", "sirius",
		"--creds", dummy, "--url", "nats://crew", "--force"); code != 0 {
		t.Fatalf("re-add sirius --force: code=%d out=%q", code, out)
	}
	if r := ctxRowByName(t, home, "sirius"); r.ID != id || r.Kind != "agent" {
		t.Fatalf("after --force re-add: id=%q kind=%q, want id=%q kind=agent (PRESERVED, not emptied)", r.ID, r.Kind, id)
	}

	// An explicit flag on re-add still overrides — preserve fills only the gaps.
	const id2 = "01BRZ3NDEKTSV4RRFFQ69G5FAV"
	if out, code := runCtx(t, home, "context", "add", "sirius",
		"--creds", dummy, "--url", "nats://crew", "--force", "--id", id2); code != 0 {
		t.Fatalf("re-add sirius with --id: code=%d out=%q", code, out)
	}
	if r := ctxRowByName(t, home, "sirius"); r.ID != id2 || r.Kind != "agent" {
		t.Fatalf("after --id re-add: id=%q kind=%q, want id=%q (overridden) kind=agent (preserved)", r.ID, r.Kind, id2)
	}
}

package selfenroll

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
)

func TestResolveBusURL(t *testing.T) {
	if got := ResolveBusURL("nats://explicit", "/does/not/exist"); got != "nats://explicit" {
		t.Fatalf("explicit --url should win: got %q", got)
	}
	dir := t.TempDir()
	if err := conninfo.Write(filepath.Join(dir, conninfo.DefaultFile), conninfo.Info{URL: "nats://disco"}); err != nil {
		t.Fatal(err)
	}
	if got := ResolveBusURL("", dir); got != "nats://disco" {
		t.Fatalf("should fall back to discovery: got %q", got)
	}
	if got := ResolveBusURL("", t.TempDir()); got != "" {
		t.Fatalf("no url, no discovery → empty: got %q", got)
	}
}

// TestCheck: the pre-flight must reject everything that would strand a mint or
// clobber state — BEFORE the bus mints — so a self-enrollment never leaves an
// unusable identity behind.
func TestCheck(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())

	if err := Check("alice", "", false); err != nil {
		t.Fatalf("clean enroll should pass: %v", err)
	}
	if err := Check("alice", "/x.creds", false); err == nil {
		t.Fatal("--out with --self should be rejected")
	}
	// a name that clictx would reject as a filename must fail before any mint
	if err := Check("a/b", "", false); err == nil {
		t.Fatal("a path-bearing name should be rejected")
	}
	// an existing context must not be silently clobbered without --force
	if err := clictx.Save(clictx.Context{Name: "bob", URL: "u", Creds: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := Check("bob", "", false); err == nil {
		t.Fatal("existing context should be rejected without --force")
	}
	if err := Check("bob", "", true); err != nil {
		t.Fatalf("--force should allow re-enroll: %v", err)
	}
}

// TestSave: enrolling yourself writes the creds into the context store (0600),
// records a context carrying the bus-minted identity, and makes it active.
func TestSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)

	credsPath, err := Save("alice", "worker", "nats://bus",
		sextant.IssuedClient{ID: "01ULID", Creds: "CREDS-BLOB"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want := filepath.Join(home, "creds", "alice.creds"); credsPath != want {
		t.Fatalf("credsPath = %q, want %q", credsPath, want)
	}
	if b, _ := os.ReadFile(credsPath); string(b) != "CREDS-BLOB" {
		t.Fatalf("creds content = %q", b)
	}
	if fi, _ := os.Stat(credsPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("creds perm = %o, want 600", fi.Mode().Perm())
	}

	c, err := clictx.Load("alice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := clictx.Context{Name: "alice", URL: "nats://bus", ID: "01ULID", Display: "alice", Kind: "worker", Creds: credsPath}
	if c != want {
		t.Fatalf("context = %+v, want %+v", c, want)
	}
	if clictx.Active() != "alice" {
		t.Fatalf("Active() = %q, want alice", clictx.Active())
	}
}

// TestSaveActivates: a self-enroll is "I am now this identity," so it activates
// the new context even if another was already active.
func TestSaveActivates(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "old", URL: "u", Creds: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := clictx.SetActive("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := Save("bob", "reviewer", "nats://b", sextant.IssuedClient{ID: "02", Creds: "X"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := clictx.Active(); got != "bob" {
		t.Fatalf("Active() = %q, want bob (self-enroll should activate)", got)
	}
}

// TestWriteContextNonActivating: an agent identity is written as a context but
// must NOT become active — the MCP server provisions its own identity without
// hijacking the operator's active context (ADR-0029). It also carries a display
// distinct from its (long, session-keyed) context handle.
func TestWriteContextNonActivating(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "operator", URL: "u", Creds: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := clictx.SetActive("operator"); err != nil {
		t.Fatal(err)
	}
	_, err := writeContext("claude-sess", "claude", "agent", "nats://b",
		sextant.IssuedClient{ID: "01AGENT", Creds: "AGENTCREDS"}, false)
	if err != nil {
		t.Fatalf("writeContext: %v", err)
	}
	if got := clictx.Active(); got != "operator" {
		t.Fatalf("Active() = %q, want operator unchanged (agent write must not activate)", got)
	}
	c, err := clictx.Load("claude-sess")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Display != "claude" || c.ID != "01AGENT" {
		t.Fatalf("context = %+v, want display=claude id=01AGENT", c)
	}
}

// TestSelfName: the env override wins, so a harness can pin the name.
func TestSelfName(t *testing.T) {
	t.Setenv("SEXTANT_SELF_NAME", "pinned")
	if got := SelfName(); got != "pinned" {
		t.Fatalf("SelfName() = %q, want the env override", got)
	}
}

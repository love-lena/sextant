package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/sextant"
)

// TestSaveSelfContext: enrolling yourself writes the creds into the context store
// (0600), records a context carrying the bus-minted identity, and makes it active.
func TestSaveSelfContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)

	credsPath, err := saveSelfContext("alice", "worker", "nats://bus",
		sextant.IssuedClient{ID: "01ULID", Creds: "CREDS-BLOB"})
	if err != nil {
		t.Fatalf("saveSelfContext: %v", err)
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

// TestSaveSelfContextActivates: a self-enroll is "I am now this identity," so it
// activates the new context even if another was already active.
func TestSaveSelfContextActivates(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "old", URL: "u", Creds: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := clictx.SetActive("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := saveSelfContext("bob", "reviewer", "nats://b", sextant.IssuedClient{ID: "02", Creds: "X"}); err != nil {
		t.Fatalf("saveSelfContext: %v", err)
	}
	if got := clictx.Active(); got != "bob" {
		t.Fatalf("Active() = %q, want bob (self-enroll should activate)", got)
	}
}

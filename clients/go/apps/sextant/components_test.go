package main

import (
	"os"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/sextant/internal/components"
)

// TestPositional extracts the component name from args, ignoring flags.
func TestPositional(t *testing.T) {
	if got := positional([]string{"dispatcher", "--all"}); got != "dispatcher" {
		t.Fatalf("positional = %q, want dispatcher", got)
	}
	if got := positional([]string{"--all"}); got != "" {
		t.Fatalf("positional of a flag-only args = %q, want empty", got)
	}
	if got := positional(nil); got != "" {
		t.Fatalf("positional of nil = %q, want empty", got)
	}
}

// TestEnsureIdentityReusesExistingCreds: when the component's creds file already
// exists, ensureIdentity is a no-op (no bus contact) — the second-run reattach.
func TestEnsureIdentityReusesExistingCreds(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	c, _ := components.Find("workflow")

	// Seed the creds file so ensureIdentity finds it and returns without minting.
	credsPath := components.CredsPath(c.Name)
	if err := os.MkdirAll(filepathDir(credsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credsPath, []byte("FAKE"), 0o600); err != nil {
		t.Fatal(err)
	}

	// With a bogus store and no bus, ensureIdentity must NOT try to connect (it
	// returns early on the existing creds). A nil error proves the no-op path.
	if err := ensureIdentity(c, t.TempDir()); err != nil {
		t.Fatalf("ensureIdentity with existing creds should be a no-op, got %v", err)
	}
}

// filepathDir avoids importing path/filepath just for one call in the test.
func filepathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

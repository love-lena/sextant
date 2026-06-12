package attest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestIdentityRoundTrip: what the SERVER writes (SaveIdentity) is exactly what
// the HOOK reads (LoadIdentity) — the lockstep contract. The hook connects with
// these creds, so a faithful round-trip is what makes hook and server co-identity.
func TestIdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Identity{Creds: "/run/sextant/claude-sess.creds", URL: "nats://127.0.0.1:4222", ID: "01KTWORKER"}

	if err := SaveIdentity(dir, sessionA, want); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	got, err := LoadIdentity(dir, sessionA)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestIdentityRewriteOnReconnect: a second SaveIdentity (the reconnect a
// context_use switch forces) overwrites the file, so the hook follows the NEW
// identity — the mechanism that kills the context_use divergence (C1).
func TestIdentityRewriteOnReconnect(t *testing.T) {
	dir := t.TempDir()
	if err := SaveIdentity(dir, sessionA, Identity{Creds: "/a.creds", URL: "nats://u", ID: "01A"}); err != nil {
		t.Fatal(err)
	}
	// The switch: server reconnects as a different identity and rewrites.
	if err := SaveIdentity(dir, sessionA, Identity{Creds: "/b.creds", URL: "nats://u", ID: "01B"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadIdentity(dir, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Creds != "/b.creds" || got.ID != "01B" {
		t.Fatalf("hook would follow the stale identity: got %+v, want the switched-to /b.creds 01B", got)
	}
}

// TestIdentityMissingDegrades: no file yet (server has not connected — turn 1
// before the first tool call) yields ErrNoIdentity, the graceful-degrade signal
// the hook keys on to exit 0 with no additionalContext.
func TestIdentityMissingDegrades(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadIdentity(dir, sessionA)
	if !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("LoadIdentity on a missing file = %v, want ErrNoIdentity", err)
	}
}

// TestIdentitySessionIsolation: different session ids keep independent identity
// files, so two concurrent sessions never read each other's identity.
func TestIdentitySessionIsolation(t *testing.T) {
	dir := t.TempDir()
	if err := SaveIdentity(dir, sessionA, Identity{Creds: "/a.creds", ID: "01A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir, "claude-code-session-BBBB"); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("a different session resolved an identity: %v, want ErrNoIdentity (isolated)", err)
	}
}

// TestIdentityCorruptIsHardError: unlike the cursor, a corrupt identity file does
// NOT degrade-to-empty (the hook cannot guess an identity) — it is a hard error
// so the hook degrades to silent rather than connect as the wrong actor.
func TestIdentityCorruptIsHardError(t *testing.T) {
	dir := t.TempDir()
	path := identityFile(dir, sessionA)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir, sessionA); err == nil || errors.Is(err, ErrNoIdentity) {
		t.Fatalf("corrupt identity should be a hard error (not ErrNoIdentity), got %v", err)
	}
}

// TestIdentityNoCredsIsError: a file with no creds path is unusable — the hook
// has nothing to connect with, so it must be an error, not a silent empty connect.
func TestIdentityNoCredsIsError(t *testing.T) {
	dir := t.TempDir()
	if err := SaveIdentity(dir, sessionA, Identity{Creds: "", URL: "nats://u", ID: "01A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir, sessionA); err == nil {
		t.Fatal("LoadIdentity accepted an identity with no creds path")
	}
}

// TestSaveIdentityNoDataDir: an unset plugin-data dir is an error the server
// logs but never fails the connect over — the hook then degrades to silent.
func TestSaveIdentityNoDataDir(t *testing.T) {
	if err := SaveIdentity("", sessionA, Identity{Creds: "/a.creds"}); err == nil {
		t.Fatal("SaveIdentity with no dataDir should error")
	}
}

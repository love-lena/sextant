package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/internal/wireapi"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

// TestClientPermissionsAllowOwnHeartbeatEcho: the per-client allow-list must let
// a client subscribe its OWN heartbeat-echo subject (sx.hb.<id>), so the echo
// watcher (TASK-126) can confirm its push path — and only its own, scoped like
// the delivery space. (Existing creds minted before this lands won't carry the
// entry; that's graceful — the echo watcher simply receives nothing.)
func TestClientPermissionsAllowOwnHeartbeatEcho(t *testing.T) {
	const id = "01HEARTBEATCLIENT0000000000"
	p := clientPermissions(id)
	want := wireapi.HeartbeatSubject(id)
	var found bool
	for _, s := range p.Sub.Allow {
		if s == want {
			found = true
		}
	}
	if !found {
		t.Errorf("clientPermissions(%q) Sub.Allow = %v; missing own heartbeat echo %q", id, p.Sub.Allow, want)
	}
}

// TestClientPermissionsDenyOtherHeartbeatEcho: the heartbeat self-sub is scoped to
// the client's OWN id and nothing wider — a client must never be able to subscribe
// ANOTHER client's beat, and the allow-list must never widen to the sx.hb.>
// wildcard. The assertion uses the real NATS matcher (SubjectsCollide), so it
// fails both on a literal sx.hb.<other> entry AND on a sx.hb.> widening that would
// collide with every client's subject — the security boundary, not just the
// string. (Mirrors the per-client scoping of sx.deliver.<id>/_INBOX.<id>.)
func TestClientPermissionsDenyOtherHeartbeatEcho(t *testing.T) {
	const id = "01HEARTBEATCLIENT0000000000"
	const other = "01HEARTBEATOTHER00000000000"
	p := clientPermissions(id)
	otherBeat := wireapi.HeartbeatSubject(other)
	for _, s := range p.Sub.Allow {
		if natsserver.SubjectsCollide(s, otherBeat) {
			t.Errorf("clientPermissions(%q) Sub.Allow entry %q permits another client's heartbeat %q; the self-sub must be own-id-scoped, never the sx.hb.> wildcard", id, s, otherBeat)
		}
	}
}

// TestProvisionInfraCredsAreOwnerOnly: the operator/enrollment credentials
// authorize identity issuance and retirement, so provisioning must leave them
// owner-only (0600) even when a reused store already holds a looser leftover —
// os.WriteFile would otherwise truncate the old file without tightening its mode.
func TestProvisionInfraCredsAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	// Simulate a reused store with a world-readable leftover operator credential.
	if err := os.WriteFile(filepath.Join(dir, "operator.creds"), []byte("stale"), 0o666); err != nil {
		t.Fatal(err)
	}
	b, err := Start(t.Context(), Config{StoreDir: dir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Shutdown()

	for _, p := range []string{OperatorCredsPath(dir), EnrollCredsPath(dir)} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o, want 600", p, perm)
		}
	}
}

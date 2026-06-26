package bus_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/protocol/wireapi"
	sextant "github.com/love-lena/sextant/sdk/go"
)

// TestSessionCredentialActsAsCaller (ADR-0044 browser-dash fix): clients.session
// issues a fresh credential whose identity is the CALLER'S OWN id, so a browser
// dash that connects with it acts AS the operator — the same id and the same
// unforgeable author prefix — rather than as a throwaway per-tab child (which
// could not read the operator's DMs and authored as a stranger). The privileged
// issuance ops are denied, so the browser-resident credential still cannot mint or
// retire even while acting as the operator.
func TestSessionCredentialActsAsCaller(t *testing.T) {
	b := startBus(t)
	ctx := readCtx(t)

	op := dialKind(t, b, "operator-seat", wireapi.KindClient)
	opID := op.ID()

	issued, err := op.MintSession(ctx)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if issued.ID != opID {
		t.Fatalf("session id = %q, want the caller's OWN id %q", issued.ID, opID)
	}
	if issued.Creds == "" {
		t.Fatal("session credential is empty")
	}

	// Connect WITH the session credential: it must authenticate as the operator's id
	// (clients.hello accepts it because the id is already registered), so a page
	// using it is the operator, not a stranger.
	sess := connectCreds(t, b, "operator-session", issued.Creds)
	if sess.ID() != opID {
		t.Fatalf("session client id = %q, want the operator's id %q", sess.ID(), opID)
	}

	// It authors AS the operator: a publish under the operator's own call prefix is
	// allowed (clientPermissions(opID)). A failure here would mean the session
	// credential could not act as the operator at all.
	rec := json.RawMessage(`{"$type":"chat.message","text":"from the operator's browser"}`)
	if err := sess.Publish(ctx, "msg.topic.session-cred-test", rec); err != nil {
		t.Fatalf("session client could not publish as the operator: %v", err)
	}

	// But it must NOT be able to mint identities — browserSessionPermissions denies
	// clients.register, so a leaked browser credential cannot dispatch new clients.
	if _, err := sess.Register(ctx, "should-not-mint", wireapi.KindAgent); err == nil {
		t.Error("session credential minted a client; a browser session must be denied the issuance ops")
	}

	// Nor may it refresh itself — clients.session is denied on the session
	// credential, so a leaked credential cannot mint a fresh one and outlive its TTL.
	if _, err := sess.MintSession(ctx); err == nil {
		t.Error("session credential minted another session; self-refresh must be denied so the TTL is the real cleanup")
	}

	// The remaining privileged issuance ops are denied on the same mechanism
	// (browserSessionPermissions.Pub.Deny): a leaked browser credential can neither
	// retire an identity nor re-point the principal, even while acting as the
	// operator. These ride the issuer call path, so connect one with the session creds.
	credsPath := filepath.Join(t.TempDir(), "session.creds")
	if err := os.WriteFile(credsPath, []byte(issued.Creds), 0o600); err != nil {
		t.Fatal(err)
	}
	iss, err := sextant.ConnectIssuer(ctx, sextant.Options{URL: b.ClientURL(), CredsPath: credsPath})
	if err != nil {
		t.Fatalf("ConnectIssuer(session creds): %v", err)
	}
	t.Cleanup(func() { _ = iss.Close() })
	if err := iss.Retire(ctx, opID); err == nil {
		t.Error("session credential retired an identity; clients.retire must be denied")
	}
	if err := iss.SetPrincipal(ctx, opID, true); err == nil {
		t.Error("session credential re-pointed the principal; principal.set must be denied")
	}
}

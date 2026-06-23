package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/protocol/wireapi"
	"github.com/nats-io/nats.go"
)

// The delegated-mint security gate (ADR-0047, TASK-188). clients.session-operator
// lets the managed dash component mint a session under the OPERATOR's id, gated
// fail-closed at the handler on a bus-stamped capability — not on kind, not on the
// caller's allow-list. These are the adversarial proofs that the gate is the only
// way through and that the dash credential carries nothing more than that one
// delegated mint.

// mintDashCredsFile mints a kind=dash credential (held-identity mint) and writes
// it to a temp file, returning the creds text, path, and the dash's minted id. A
// kind=dash mint is the one the bus gives dashComponentPermissions and stamps with
// CapMintOperatorSession (the capability the handler gates on).
func mintDashCredsFile(t *testing.T, b *Bus, name string) (creds, path, id string) {
	t.Helper()
	creds, id, err := b.MintClient(t.Context(), name, wireapi.KindDash)
	if err != nil {
		t.Fatalf("MintClient(kind=dash): %v", err)
	}
	path = filepath.Join(t.TempDir(), id+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return creds, path, id
}

// TestSessionOperatorMintsUnderPrincipal: dash.creds CAN mint a session whose id is
// the OPERATOR's (the principal), carrying browserSessionPermissions — the AC#3
// happy path. It exercises the handler directly (caller = the dash's minted id) so
// the gate's caller is explicit, then connects with the minted session to prove the
// output is issuance-denied (cannot register).
func TestSessionOperatorMintsUnderPrincipal(t *testing.T) {
	b := startTestBus(t)
	ctx := testCtx(t)

	// A real operator seat as the principal (the bus bootstraps the principal to
	// the reserved "operator" id, but a delegated session must land under a
	// registered client id we can connect as — so designate a minted seat).
	opSeat := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: opSeat, Force: true}); err != nil {
		t.Fatalf("designate principal: %v", err)
	}

	_, _, dashID := mintDashCredsFile(t, b, "dash")

	raw, err := b.opClientsSessionOperator(ctx, dashID)
	if err != nil {
		t.Fatalf("dash.creds must be able to mint an operator session: %v", err)
	}
	var out wireapi.RegisterOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode session-operator output: %v", err)
	}
	if out.ID != opSeat {
		t.Fatalf("minted session id = %q, want the principal/operator id %q", out.ID, opSeat)
	}
	if out.Creds == "" {
		t.Fatal("minted operator session credential is empty")
	}

	// The minted credential acts AS the operator seat but is issuance-denied: a
	// register publish over it raises a NATS-layer Permissions Violation
	// (browserSessionPermissions denies clients.register), so it never even reaches
	// the handler — the credential is the fence.
	credsPath := filepath.Join(t.TempDir(), "opsession.creds")
	if err := os.WriteFile(credsPath, []byte(out.Creds), 0o600); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 2)
	nc := connectWithCreds(
		t, b, opSeat, credsPath,
		nats.CustomInboxPrefix(wireapi.InboxPrefix(opSeat)),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errCh <- e:
			default:
			}
		}),
	)
	if err := nc.Publish(wireapi.CallSubject(opSeat, wireapi.OpClientsRegister), []byte("{}")); err != nil {
		t.Fatalf("publish register: sync error: %v", err)
	}
	_ = nc.Flush()
	select {
	case e := <-errCh:
		if !strings.Contains(e.Error(), "Permissions Violation") {
			t.Errorf("minted operator session register: want a permissions violation, got: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Error("the minted operator session must be issuance-denied (browserSessionPermissions denies clients.register)")
	}
}

// TestSessionOperatorMintedCredsCarryBrowserPermissions: the minted operator
// session's permission set is exactly browserSessionPermissions(principal) — so it
// reads the operator's delivery/inbox/heartbeat space (acts AS the operator) but
// denies the privileged issuance ops at the NATS layer. Asserted on the perms
// function the mint path uses, so it can't silently widen.
func TestSessionOperatorMintedCredsCarryBrowserPermissions(t *testing.T) {
	const op = "01OPERATORSEAT0000000000000"
	got := browserSessionPermissions(op)
	// Acts as the operator: its own call prefix is allowed.
	if len(got.Pub.Allow) == 0 || got.Pub.Allow[0] != wireapi.APIPrefix+op+".>" {
		t.Errorf("operator session must author under the operator's own prefix; Pub.Allow=%v", got.Pub.Allow)
	}
	// Issuance-denied: register/retire/session/session-operator/principal.set.
	for _, denied := range []string{
		wireapi.OpClientsRegister,
		wireapi.OpClientsRetire,
		wireapi.OpClientsSession,
		wireapi.OpClientsSessionOperator,
		wireapi.OpPrincipalSet,
	} {
		want := wireapi.CallSubject(op, denied)
		found := false
		for _, d := range got.Pub.Deny {
			if d == want {
				found = true
			}
		}
		if !found {
			t.Errorf("browserSessionPermissions(%q) Pub.Deny missing %q; a session cred must not %s", op, want, denied)
		}
	}
}

// TestSessionOperatorDeniedToNormalClient: a NORMAL registered client (no
// capability) is DENIED at the handler — even though its own wildcard pub-allow
// lets the publish through (the publish is NOT the gate). This is the escalation
// hole staying closed: only the bus-stamped capability admits the delegated mint.
func TestSessionOperatorDeniedToNormalClient(t *testing.T) {
	b := startTestBus(t)
	ctx := testCtx(t)

	opSeat := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: opSeat, Force: true}); err != nil {
		t.Fatalf("designate principal: %v", err)
	}

	normal := mintKind(t, b, wireapi.KindClient)
	if _, err := b.opClientsSessionOperator(ctx, normal); err == nil {
		t.Fatal("a normal client with no capability minted an operator session; the capability gate must deny it")
	}
}

// TestSessionOperatorPublishDeniedToNormalClient: the NATS-layer proof of the
// "publish is not the gate" point — a normal client's allow-list DOES let it
// publish to its OWN session-operator subject (sx.api.<id>.clients.session-operator
// is under sx.api.<id>.>), so the request reaches the handler and the handler is
// what rejects it. The request returns a response carrying the handler's error,
// rather than a NATS permissions violation. (Mirrors the design's leg #3 at the
// wire layer.)
func TestSessionOperatorPublishReachesHandlerForNormalClient(t *testing.T) {
	b := startTestBus(t)

	opSeat := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: opSeat, Force: true}); err != nil {
		t.Fatalf("designate principal: %v", err)
	}

	nc, id := connectClient(t, b, "normal-client")
	resp := call(t, nc, id, wireapi.OpClientsSessionOperator, struct{}{})
	if resp.Error == "" {
		t.Fatal("a normal client's delegated-mint call returned no error; the handler capability gate must reject it")
	}
	if !strings.Contains(resp.Error, "capability") {
		t.Errorf("delegated-mint denial should name the missing capability; got %q", resp.Error)
	}
}

// TestSessionOperatorDeniedNoPrincipal: with no principal designated, the
// delegated mint fails loud (there is no operator id to mint under) rather than
// minting under an empty id.
func TestSessionOperatorDeniedNoPrincipal(t *testing.T) {
	b := startTestBus(t)
	ctx := testCtx(t)

	// Clear the bootstrap principal so readPrincipal returns empty.
	if err := b.backend.Delete(ctx, sx.BucketMeta, sx.MetaKeyPrincipal); err != nil {
		t.Fatalf("clear principal: %v", err)
	}

	_, _, dashID := mintDashCredsFile(t, b, "dash")
	if _, err := b.opClientsSessionOperator(ctx, dashID); err == nil {
		t.Fatal("delegated mint with no principal designated must fail loud")
	}
}

// TestDashCredsCannotIssueAtNATSLayer: dash.creds carries dashComponentPermissions
// — a SINGLE delegated-mint pub-allow and the call-reply inbox, nothing else. So at
// the NATS layer it cannot publish register / retire / principal.set / clients.
// session / ordinary messages: each raises a Permissions Violation. This is the
// AC#3 "cannot register/retire/principal.set" proof at the credential layer (the
// dash holds no operator/issuer authority).
func TestDashCredsCannotIssueAtNATSLayer(t *testing.T) {
	b := startTestBus(t)
	_, dashPath, dashID := mintDashCredsFile(t, b, "dash")

	errCh := make(chan error, 8)
	nc, err := nats.Connect(
		b.ClientURL(),
		nats.UserCredentials(dashPath),
		nats.Name(dashID),
		nats.CustomInboxPrefix(wireapi.InboxPrefix(dashID)),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errCh <- e:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("connect dash.creds: %v", err)
	}
	t.Cleanup(nc.Close)

	wantViolation := func(subject string) {
		t.Helper()
		drainErrCh(errCh)
		if err := nc.Publish(subject, []byte("{}")); err != nil {
			t.Fatalf("publish %s: sync error: %v", subject, err)
		}
		_ = nc.Flush()
		select {
		case e := <-errCh:
			if !strings.Contains(e.Error(), "Permissions Violation") {
				t.Errorf("publish %s: expected a permissions violation, got: %v", subject, e)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("dash.creds must be denied publishing %s (it holds no issuer authority)", subject)
		}
	}

	// Each privileged issuance op + an ordinary message + clients.session itself:
	// dashComponentPermissions grants none of them, only session-operator.
	wantViolation(wireapi.CallSubject(dashID, wireapi.OpClientsRegister))
	wantViolation(wireapi.CallSubject(dashID, wireapi.OpClientsRetire))
	wantViolation(wireapi.CallSubject(dashID, wireapi.OpPrincipalSet))
	wantViolation(wireapi.CallSubject(dashID, wireapi.OpClientsSession))
	wantViolation(wireapi.CallSubject(dashID, wireapi.OpMessagePublish))

	// It must NOT be allowed to subscribe the sx.hb echo space (clears TASK-185):
	// dashComponentPermissions grants no sx.deliver / sx.hb subscription.
	drainErrCh(errCh)
	if _, err := nc.SubscribeSync(wireapi.HeartbeatSubject(dashID)); err != nil {
		t.Fatalf("subscribe sx.hb sync error: %v", err)
	}
	_ = nc.Flush()
	select {
	case e := <-errCh:
		if !strings.Contains(e.Error(), "Permissions Violation") {
			t.Errorf("subscribe sx.hb: expected a permissions violation, got: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Error("dash.creds must be denied the sx.hb subscription (TASK-185: it does not heartbeat)")
	}
}

// TestDashCredsAllowedDelegatedMintAtNATSLayer: the one thing dash.creds CAN
// publish — its own session-operator call subject — raises NO permissions
// violation (dashComponentPermissions allows exactly that subject).
func TestDashCredsAllowedDelegatedMintAtNATSLayer(t *testing.T) {
	b := startTestBus(t)
	_, dashPath, dashID := mintDashCredsFile(t, b, "dash")

	errCh := make(chan error, 4)
	nc, err := nats.Connect(
		b.ClientURL(),
		nats.UserCredentials(dashPath),
		nats.Name(dashID),
		nats.CustomInboxPrefix(wireapi.InboxPrefix(dashID)),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errCh <- e:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("connect dash.creds: %v", err)
	}
	t.Cleanup(nc.Close)

	drainErrCh(errCh)
	if err := nc.Publish(wireapi.CallSubject(dashID, wireapi.OpClientsSessionOperator), []byte("{}")); err != nil {
		t.Fatalf("publish session-operator: sync error: %v", err)
	}
	_ = nc.Flush()
	select {
	case e := <-errCh:
		if strings.Contains(e.Error(), "Permissions Violation") {
			t.Errorf("dash.creds must be allowed to publish its session-operator subject, got: %v", e)
		}
	case <-time.After(300 * time.Millisecond):
		// No violation — the allow worked.
	}
}

// TestMintOnBehalfDashKindRejected: a mint-on-behalf (non-operator) caller
// requesting kind=dash is REJECTED — no capability escalation. Only a held-identity
// (operator) mint may grant the dash capability; a registered client trying to mint
// a kind=dash child must fail, so a compromised client cannot manufacture a
// capability-bearing identity for itself.
func TestMintOnBehalfDashKindRejected(t *testing.T) {
	b := startTestBus(t)
	ctx := testCtx(t)

	caller := mintKind(t, b, wireapi.KindClient)
	in, err := json.Marshal(wireapi.RegisterInput{DisplayName: "sneaky-dash", Kind: wireapi.KindDash})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.opClientsRegister(ctx, caller, in); err == nil {
		t.Fatal("a mint-on-behalf caller minted a kind=dash identity; the dash capability must be operator-mint-only")
	}
}

// TestDashMintStampsCapability: a held-identity kind=dash mint records the
// CapMintOperatorSession capability on the durable ClientEntry — the forge-proof
// field the handler gates on (mirrors SpawnedBy's bus-stamped discipline).
func TestDashMintStampsCapability(t *testing.T) {
	b := startTestBus(t)
	ctx := testCtx(t)
	_, _, dashID := mintDashCredsFile(t, b, "dash")

	val, _, err := b.backend.Get(ctx, sx.BucketClients, dashID)
	if err != nil {
		t.Fatalf("read dash record: %v", err)
	}
	var e wireapi.ClientEntry
	if err := json.Unmarshal(val, &e); err != nil {
		t.Fatalf("decode dash record: %v", err)
	}
	found := false
	for _, c := range e.Capabilities {
		if c == wireapi.CapMintOperatorSession {
			found = true
		}
	}
	if !found {
		t.Errorf("dash record Capabilities = %v, want it to carry %q", e.Capabilities, wireapi.CapMintOperatorSession)
	}
}

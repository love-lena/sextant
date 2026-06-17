package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/nats.go"
)

// freeTCPPort reserves a free localhost TCP port and returns it. The probe
// listener is closed before returning, so NATS can bind the port — there is a
// small reuse window, acceptable for an in-process test (the same pattern bus.go
// uses to probe a recorded port).
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startHubWithLeafListen starts a hub bus (JetStream on) with a leaf listener on
// a free port and returns it plus the nats-leaf:// URL a leaf links to.
func startHubWithLeafListen(t *testing.T) (hub *Bus, leafURL string) {
	t.Helper()
	leafPort := freeTCPPort(t)
	hub, err := Start(t.Context(), Config{
		StoreDir:       t.TempDir(),
		LeafListenAddr: fmt.Sprintf("127.0.0.1:%d", leafPort),
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Shutdown)
	return hub, fmt.Sprintf("nats-leaf://127.0.0.1:%d", leafPort)
}

// startLeafLinkedTo starts a leaf bus (JetStream off) that links to hubURL using
// the bundle + link credential the hub wrote into hubStore, and waits for the
// link to come up. It returns the leaf.
func startLeafLinkedTo(t *testing.T, hubStore, hubURL string) *Bus {
	t.Helper()
	leaf, err := Start(t.Context(), Config{
		StoreDir:      t.TempDir(),
		LeafRemoteURL: hubURL,
		LeafBundle:    LeafBundlePath(hubStore),
		LeafCreds:     LeafLinkCredsPath(hubStore),
	})
	if err != nil {
		t.Fatalf("start leaf: %v", err)
	}
	t.Cleanup(leaf.Shutdown)
	linkCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := leaf.WaitLeafLinked(linkCtx); err != nil {
		t.Fatalf("leaf link did not come up: %v", err)
	}
	return leaf
}

// connectAgentToLeaf mints a per-client credential on the HUB (the sole minter)
// and connects with it to the LEAF's client listener, the way a remote agent
// joins: hub-minted creds, leaf-local connection. It returns the connection and
// the agent's bus-minted id.
func connectAgentToLeaf(t *testing.T, hub, leaf *Bus, name string) (*nats.Conn, string) {
	t.Helper()
	creds, id, err := hub.MintClient(t.Context(), name, "agent")
	if err != nil {
		t.Fatalf("hub mint %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), id+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(
		leaf.ClientURL(),
		nats.UserCredentials(path),
		nats.Name(id),
		nats.CustomInboxPrefix(wireapi.InboxPrefix(id)),
	)
	if err != nil {
		t.Fatalf("agent %s connect to leaf: %v", id, err)
	}
	t.Cleanup(nc.Close)
	return nc, id
}

// TestLeafFederatesWireAPIPreservingIdentity is the load-bearing acceptance gate
// (ADR-0038): an agent connected to the LEAF makes a wire-API publish + artifact
// round-trip that federates to the hub, and the hub stamps the author from the
// subject token — so identity is preserved end-to-end across the leaf link. The
// JetStream that backs both the messages log and the artifacts bucket lives only
// on the hub; the leaf has none.
func TestLeafFederatesWireAPIPreservingIdentity(t *testing.T) {
	hub, hubURL := startHubWithLeafListen(t)
	leaf := startLeafLinkedTo(t, hub.store, hubURL)
	nc, id := connectAgentToLeaf(t, hub, leaf, "leaf-agent")

	// message.publish federates to the hub; the hub stamps author == subject id.
	subj := sx.TopicSubject("plan")
	resp := call(t, nc, id, wireapi.OpMessagePublish, wireapi.PublishInput{
		Subject: subj, Record: json.RawMessage(`{"from":"leaf"}`),
	})
	if resp.Error != "" {
		t.Fatalf("leaf publish federated to hub failed: %s", resp.Error)
	}
	var pub wireapi.PublishOutput
	mustJSON(t, resp.Result, &pub)
	if pub.ID == "" || pub.Seq == 0 {
		t.Fatalf("bad publish output across the leaf: %+v", pub)
	}

	// Read it back through the leaf; the frame the hub persisted carries the agent
	// as author — identity preserved across the link.
	var rd wireapi.ReadOutput
	mustJSON(t, call(t, nc, id, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subj, Limit: 10}).Result, &rd)
	if len(rd.Messages) != 1 {
		t.Fatalf("read across the leaf returned %d messages, want 1", len(rd.Messages))
	}
	if rd.Messages[0].Author != id {
		t.Errorf("hub-stamped author = %q, want the leaf agent id %q", rd.Messages[0].Author, id)
	}

	// Confirm the hub — not the leaf — holds the JetStream-backed frame, by reading
	// it from the hub's own operator connection. (The leaf has no JetStream.)
	var hubRead wireapi.ReadOutput
	mustJSON(t, callHub(t, hub, id, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subj, Limit: 10}), &hubRead)
	if len(hubRead.Messages) != 1 || hubRead.Messages[0].Author != id {
		t.Fatalf("hub does not hold the federated frame with the agent author: %+v", hubRead.Messages)
	}

	// artifact round-trip across the leaf: create + get, author stamped on the hub.
	if resp := call(t, nc, id, wireapi.OpArtifactCreate, wireapi.ArtifactCreateInput{
		Name: "leaf-plan", Record: json.RawMessage(`{"title":"from the leaf"}`),
	}); resp.Error != "" {
		t.Fatalf("leaf artifact.create federated to hub failed: %s", resp.Error)
	}
	var got wireapi.ArtifactGetOutput
	mustJSON(t, call(t, nc, id, wireapi.OpArtifactGet, wireapi.ArtifactGetInput{Name: "leaf-plan"}).Result, &got)
	if string(got.Record) != `{"title":"from the leaf"}` {
		t.Errorf("artifact round-trip across the leaf = %s", got.Record)
	}
}

// TestLeafEnforcesPerClientPermsLocally pins the trust model's load-bearing claim
// (ADR-0038): the per-client allow-list is enforced AT THE LEAF, so an agent
// publishing under a DIFFERENT id (sx.api.<other>.publish) is rejected at the leaf
// with a permissions violation — the call never reaches the hub. This is what
// makes the hub's subject-derived author stamp trustworthy even for leaf clients.
func TestLeafEnforcesPerClientPermsLocally(t *testing.T) {
	hub, hubURL := startHubWithLeafListen(t)
	leaf := startLeafLinkedTo(t, hub.store, hubURL)

	errCh := make(chan error, 4)
	creds, id, err := hub.MintClient(t.Context(), "leaf-agent", "agent")
	if err != nil {
		t.Fatalf("hub mint: %v", err)
	}
	path := filepath.Join(t.TempDir(), id+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(
		leaf.ClientURL(),
		nats.UserCredentials(path),
		nats.Name(id),
		nats.CustomInboxPrefix(wireapi.InboxPrefix(id)),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errCh <- e:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("agent connect to leaf: %v", err)
	}
	t.Cleanup(nc.Close)

	// Publish under a foreign id: outside this credential's allow-list
	// (sx.api.<own-id>.>), so the leaf rejects it before any federation.
	if err := nc.Publish(wireapi.CallSubject("someone-else", wireapi.OpMessagePublish), []byte("{}")); err != nil {
		t.Fatalf("publish returned a sync error: %v", err)
	}
	_ = nc.Flush()
	select {
	case e := <-errCh:
		if !strings.Contains(e.Error(), "Permissions Violation") {
			t.Errorf("expected a permissions violation at the leaf, got: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected a permissions-violation for publishing under a foreign id at the leaf")
	}
}

// TestLeafAgentPresenceViaHeartbeat verifies the presence convergence (ADR-0036
// ⇄ ADR-0038): a leaf-connected agent is NOT in the hub's connection table (the
// hub sees only the leaf link), so without the heartbeat it would read offline.
// Its federated clients.heartbeat stamps last_seen on the hub registry, and the
// hub's dual-source presence rule then reports it ONLINE. No new presence
// machinery — the merged heartbeat already crosses the leaf.
func TestLeafAgentPresenceViaHeartbeat(t *testing.T) {
	hub, hubURL := startHubWithLeafListen(t)
	leaf := startLeafLinkedTo(t, hub.store, hubURL)
	nc, id := connectAgentToLeaf(t, hub, leaf, "leaf-beater")

	// Before any beat, the hub does not see the leaf agent in its Connz, so its
	// presence rests on last_seen alone — empty here, hence offline. (Sanity-check
	// the precondition so the test proves the heartbeat is what flips it.)
	if onlinePresence(t, hub, id) {
		t.Fatal("a leaf agent with no beat should not be online on the hub yet (it is not in the hub Connz)")
	}

	// Beat: federates to the hub's opClientsHeartbeat, which stamps last_seen.
	if resp := call(t, nc, id, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: 1}); resp.Error != "" {
		t.Fatalf("leaf agent heartbeat federated to hub failed: %s", resp.Error)
	}

	// The hub's clients.list now derives the leaf agent online from the fresh beat.
	if !onlinePresence(t, hub, id) {
		t.Errorf("a leaf agent with a fresh beat should be online on the hub (dual-source presence)")
	}
}

// TestLeafHeartbeatEchoFederatesBack is the nice-to-have (ADR-0038 (d)): the
// hub's heartbeat echo on sx.hb.<id> federates back across the leaf to the agent,
// so the agent's own push-path check works the same behind a leaf as direct.
func TestLeafHeartbeatEchoFederatesBack(t *testing.T) {
	hub, hubURL := startHubWithLeafListen(t)
	leaf := startLeafLinkedTo(t, hub.store, hubURL)
	nc, id := connectAgentToLeaf(t, hub, leaf, "leaf-echo")

	echoSub, err := nc.SubscribeSync(wireapi.HeartbeatSubject(id))
	if err != nil {
		t.Fatalf("subscribe echo on the leaf: %v", err)
	}
	_ = nc.Flush()

	if resp := call(t, nc, id, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: 42}); resp.Error != "" {
		t.Fatalf("heartbeat: %s", resp.Error)
	}
	msg, err := echoSub.NextMsg(3 * time.Second)
	if err != nil {
		t.Fatalf("heartbeat echo did not federate back to the leaf agent: %v", err)
	}
	var echo wireapi.HeartbeatEcho
	mustJSON(t, msg.Data, &echo)
	if echo.Seq != 42 {
		t.Errorf("federated echo Seq = %d, want 42", echo.Seq)
	}
}

// TestLeafCannotMint pins the key-custody half of the trust model (ADR-0038): the
// leaf holds the hub's PUBLIC account JWTs but no signing seed, so it has no
// identity material to mint with — minting stays at the hub. The leaf's identity
// is nil (startLeaf never loads seeds), which is exactly what makes a local mint
// path impossible to construct. The hub-only operations (mint, drain) must return
// a CLEAN error on a leaf Bus, never nil-deref the absent identity/backend.
func TestLeafCannotMint(t *testing.T) {
	hub, hubURL := startHubWithLeafListen(t)
	leaf := startLeafLinkedTo(t, hub.store, hubURL)

	if leaf.ident != nil {
		t.Error("a leaf must hold no signing identity (it cannot be the minter)")
	}
	// The hub, by contrast, holds its signing identity and can mint.
	if hub.ident == nil {
		t.Error("the hub must hold its signing identity (it is the sole minter)")
	}
	// MintClient on a leaf returns a clean error, not a panic on the nil identity.
	if _, _, err := leaf.MintClient(t.Context(), "should-fail", "agent"); err == nil {
		t.Error("MintClient on a leaf must return an error (minting is a hub act)")
	}
	// Drain on a leaf likewise returns a clean error, not a nil-deref on opConn.
	if err := leaf.Drain(); err == nil {
		t.Error("Drain on a leaf must return an error (the hub drains its clients)")
	}
}

// TestLeafLinkCredScopedToFederationSet pins fix-1 of the trust model (ADR-0038):
// the leaf-link credential is scoped to the federation set ONLY, so even though it
// can carry every agent's traffic, it cannot reach an operator/admin/mint subject.
// We connect with the actual leaf-link.creds DIRECTLY to the hub (the worst case —
// a leaf box that connects its link credential straight to the hub) and confirm the
// link is denied the WHOLE reserved surface of BOTH reserved identities — for each
// of operator and enroll, publishing the issuance call (sx.api.<id>.>), subscribing
// the reply inbox (_INBOX.<id>.>), AND subscribing the push-delivery space
// (sx.deliver.<id>.>) are each rejected — while a federation-set publish under an
// ordinary id is permitted. This proves the link credential is not an
// operator/enroll key: it cannot escalate to issuance (by call or by reply
// eavesdrop) and cannot eavesdrop the operator's push streams.
func TestLeafLinkCredScopedToFederationSet(t *testing.T) {
	hub, _ := startHubWithLeafListen(t)

	errCh := make(chan error, 4)
	nc, err := nats.Connect(
		hub.ClientURL(),
		nats.UserCredentials(LeafLinkCredsPath(hub.store)),
		nats.Name("leaf-link-direct"),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errCh <- e:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("connect leaf-link cred directly to hub: %v", err)
	}
	t.Cleanup(nc.Close)

	// wantViolation publishes/subscribes the given subject and asserts the
	// credential's allow/deny raises a Permissions Violation. denied says what the
	// subject is (for the failure message).
	wantViolation := func(action func() error, denied string) {
		t.Helper()
		drainErrCh(errCh)
		if err := action(); err != nil {
			t.Fatalf("%s: sync error: %v", denied, err)
		}
		_ = nc.Flush()
		select {
		case e := <-errCh:
			if !strings.Contains(e.Error(), "Permissions Violation") {
				t.Errorf("expected a permissions violation on %s, got: %v", denied, e)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("the leaf-link credential must be denied %s", denied)
		}
	}
	pub := func(subject string) func() error {
		return func() error { return nc.Publish(subject, []byte("{}")) }
	}
	sub := func(subject string) func() error {
		return func() error { _, err := nc.SubscribeSync(subject); return err }
	}

	// Both reserved identities (operator AND enroll) are OUTSIDE the federation set,
	// and the link is denied their WHOLE surface in BOTH directions: it can neither
	// CALL their issuance subjects (sx.api.<id>.>), nor READ their reply inboxes
	// (_INBOX.<id>.>), nor EAVESDROP their push streams (sx.deliver.<id>.>). The call
	// deny stops it asking the bus to mint/retire/claim; the inbox deny stops it
	// intercepting an issuance reply's freshly-minted credential; the deliver deny
	// stops it reading the operator's principal.watch / artifact.watch deliveries.
	// Together they keep "a compromised leaf touches no operator/enroll subject" exact.
	for _, reserved := range []string{wireapi.OperatorID, wireapi.EnrollID} {
		wantViolation(pub(wireapi.CallSubject(reserved, wireapi.OpClientsRegister)), "publish to sx.api."+reserved+".>")
		wantViolation(sub(wireapi.InboxPrefix(reserved)+".>"), "subscribe _INBOX."+reserved+".>")
		wantViolation(sub(wireapi.DeliverPrefix+reserved+".>"), "subscribe sx.deliver."+reserved+".>")
	}

	// A federation-set publish (sx.api.<id>.>) under an ordinary id IS within the
	// link's grant — the link must carry per-client traffic — so it must NOT raise a
	// violation. (Fire-and-forget; we only assert no permissions error follows.)
	drainErrCh(errCh)
	if err := nc.Publish(wireapi.CallSubject("01LEAFLINKFEDOK000000000000", wireapi.OpMessagePublish), []byte("{}")); err != nil {
		t.Fatalf("federation-set publish sync error: %v", err)
	}
	_ = nc.Flush()
	select {
	case e := <-errCh:
		if strings.Contains(e.Error(), "Permissions Violation") {
			t.Errorf("a federation-set publish must be allowed on the link credential, got: %v", e)
		}
	case <-time.After(300 * time.Millisecond):
		// No error within the window — the federation-set publish was permitted.
	}
}

// TestLeafTrustBoundaryAuthorIsLeafEnforced documents the accepted residual trust
// boundary (ADR-0038): the hub stamps the author from the call subject, and the
// per-client SCOPING that makes that honest is enforced at the LEAF's edge on each
// agent's own credential — NOT on the link. So a trusted leaf forwarding a call is
// the trust boundary: were a leaf box compromised, its link credential could
// forward a call subject for any id and the hub would stamp that id as author. We
// pin the load-bearing half that DOES hold — the author the hub records equals the
// subject id the leaf forwarded — by forwarding a call straight over the link
// credential and reading the stamped author back from the hub. This is why a leaf
// runs only on a trusted box over a secure transport.
func TestLeafTrustBoundaryAuthorIsLeafEnforced(t *testing.T) {
	hub, _ := startHubWithLeafListen(t)

	// First issue a real victim identity at the hub, so the subject id names a known
	// client (the registry record exists). The leaf-link credential then forwards a
	// publish under that id.
	_, victimID, err := hub.MintClient(t.Context(), "victim", "agent")
	if err != nil {
		t.Fatalf("hub mint victim: %v", err)
	}

	nc, err := nats.Connect(
		hub.ClientURL(),
		nats.UserCredentials(LeafLinkCredsPath(hub.store)),
		nats.Name("leaf-link-forwarder"),
		nats.CustomInboxPrefix(wireapi.InboxPrefix(victimID)),
	)
	if err != nil {
		t.Fatalf("connect leaf-link cred to hub: %v", err)
	}
	t.Cleanup(nc.Close)

	// A call under victimID over the link credential. This connects the link
	// credential straight to the hub (not through a real leaf bridge), which models
	// the credential's SCOPE — what subjects a holder of leaf-link.creds may carry —
	// rather than the full leaf topology. That scope is the load-bearing thing for
	// this boundary: the link credential is authorized to carry any agent's call
	// subject, and the hub stamps author = that subject id. The end-to-end topology
	// path is covered by TestLeafFederatesWireAPIPreservingIdentity.
	subj := sx.TopicSubject("trust-boundary")
	resp := call(t, nc, victimID, wireapi.OpMessagePublish, wireapi.PublishInput{
		Subject: subj, Record: json.RawMessage(`{"forwarded":true}`),
	})
	if resp.Error != "" {
		t.Fatalf("link-forwarded publish under victim id failed: %s", resp.Error)
	}

	var rd wireapi.ReadOutput
	mustJSON(t, callHub(t, hub, victimID, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subj, Limit: 10}), &rd)
	if len(rd.Messages) != 1 || rd.Messages[0].Author != victimID {
		t.Fatalf("hub must stamp author = the subject id the leaf forwarded (the trust boundary): %+v", rd.Messages)
	}
}

// drainErrCh empties any buffered async errors so a later select sees only new ones.
func drainErrCh(ch chan error) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// callHub invokes an op directly on the hub's in-process operator connection,
// bypassing the leaf — used to assert the hub itself holds the federated state.
func callHub(t *testing.T, hub *Bus, id, op string, input any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s input: %v", op, err)
	}
	msg, err := hub.opConn.Request(wireapi.CallSubject(id, op), data, 5*time.Second)
	if err != nil {
		t.Fatalf("hub call %s: %v", op, err)
	}
	var resp wireapi.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatalf("unmarshal %s response: %v", op, err)
	}
	if resp.Error != "" {
		t.Fatalf("hub call %s: %s", op, resp.Error)
	}
	return resp.Result
}

// onlinePresence reports whether the hub's clients.list shows id online — the
// dual-source presence the leaf agent rides (ADR-0036).
func onlinePresence(t *testing.T, hub *Bus, id string) bool {
	t.Helper()
	var out wireapi.ClientsListOutput
	mustJSON(t, callHub(t, hub, wireapi.OperatorID, wireapi.OpClientsList, struct{}{}), &out)
	for _, e := range out.Clients {
		if e.ID == id {
			return e.Presence == wireapi.PresenceOnline
		}
	}
	return false
}

package bus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// displayNameOf extracts a credential's human display_name from its JWT tags.
func displayNameOf(uc *jwt.UserClaims) string {
	for _, tag := range uc.Tags {
		if n, ok := wireapi.DecodeDisplayNameTag(tag); ok {
			return n
		}
	}
	return ""
}

func startTestBus(t *testing.T) *Bus {
	t.Helper()
	b, err := Start(t.Context(), Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// mintCredsFile mints a fresh per-client credential whose display_name is name
// and writes it to a temp file, returning the creds text, the file path, and the
// bus-minted ULID id that is the credential's authenticated identity — the
// subject a client must use for its calls and delivery subscriptions under the
// per-client allow-list.
func mintCredsFile(t *testing.T, b *Bus, name string) (creds, path, id string) {
	t.Helper()
	creds, id, err := b.MintClient(t.Context(), name, "test")
	if err != nil {
		t.Fatalf("MintClient(%s): %v", name, err)
	}
	path = filepath.Join(t.TempDir(), id+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return creds, path, id
}

// connectWithCreds connects using an existing credentials file (no mint). id is
// the authenticated identity (used as the connection name and in errors).
func connectWithCreds(t *testing.T, b *Bus, id, path string, opts ...nats.Option) *nats.Conn {
	t.Helper()
	all := append([]nats.Option{nats.UserCredentials(path), nats.Name(id)}, opts...)
	nc, err := nats.Connect(b.ClientURL(), all...)
	if err != nil {
		t.Fatalf("connect as %s: %v", id, err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// connectClient mints a fresh per-client credential (display_name name) and
// connects with it, returning the connection and the bus-minted ULID id that is
// its authenticated identity — the subject the client must use for its calls.
func connectClient(t *testing.T, b *Bus, name string, opts ...nats.Option) (*nats.Conn, string) {
	t.Helper()
	_, path, id := mintCredsFile(t, b, name)
	// Match the SDK: a per-client inbox so call replies land where the credential
	// allows subscribing (_INBOX.<id>.>). Without it nc.Request uses the shared
	// _INBOX prefix the allow-list denies and every call times out.
	opts = append(opts, nats.CustomInboxPrefix(wireapi.InboxPrefix(id)))
	return connectWithCreds(t, b, id, path, opts...), id
}

func TestStartBootstrapsBuckets(t *testing.T) {
	b := startTestBus(t)
	// Check via the bus's own operator connection: under the per-client allow-list
	// clients have no direct JetStream access, so bootstrap is an operator-side
	// fact, not something a client can observe.
	js, err := jetstream.New(b.opConn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	for _, spec := range sx.Buckets() {
		if _, err := js.KeyValue(ctx, spec.Name); err != nil {
			t.Errorf("bucket %s not bootstrapped: %v", spec.Name, err)
		}
	}
}

// TestNoOperatorOnlyBucket guards the v1 decision (ADR-0012): there is no
// operator-only bucket — system state will get its own NATS account rather than
// a same-account bucket guarded by deny-lists. Looking from the operator
// connection (which can see every bucket in the account) proves sx_system
// genuinely does not exist, not merely that a client is denied a peek.
func TestNoOperatorOnlyBucket(t *testing.T) {
	b := startTestBus(t)
	js, err := jetstream.New(b.opConn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.KeyValue(testCtx(t), "sx_system"); err == nil {
		t.Error("sx_system should not exist in v1 (operator-only state is deferred to a separate account)")
	}
}

// TestPerClientIdentity is the point of JWT auth: distinct clients get distinct,
// verified identities, so ops are attributable.
func TestPerClientIdentity(t *testing.T) {
	b := startTestBus(t)
	alice, alicePath, _ := mintCredsFile(t, b, "alice")
	bob, bobPath, _ := mintCredsFile(t, b, "bob")
	ac, err := jwt.DecodeUserClaims(parseJWT(t, alice))
	if err != nil {
		t.Fatal(err)
	}
	bc, err := jwt.DecodeUserClaims(parseJWT(t, bob))
	if err != nil {
		t.Fatal(err)
	}
	if ac.Subject == bc.Subject {
		t.Error("two clients were issued the same identity")
	}
	// The primary id is a bus-minted ULID (distinct per client); the human
	// display_name rides in the JWT tags.
	if ac.Name == bc.Name {
		t.Errorf("two clients got the same id %q", ac.Name)
	}
	if dn := displayNameOf(ac); dn != "alice" {
		t.Errorf("alice display_name = %q, want alice", dn)
	}
	if dn := displayNameOf(bc); dn != "bob" {
		t.Errorf("bob display_name = %q, want bob", dn)
	}
	// Both must actually connect with their own credential.
	_ = connectWithCreds(t, b, "alice", alicePath)
	_ = connectWithCreds(t, b, "bob", bobPath)
}

// TestMintGivesDistinctIDs: the bus mints each client a fresh ULID id, so even
// two clients sharing a display_name get distinct, non-colliding identities
// (the old silent-collision footgun is gone — ids are no longer the human name).
func TestMintGivesDistinctIDs(t *testing.T) {
	b := startTestBus(t)
	_, id1, err := b.MintClient(t.Context(), "dup", "test")
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}
	_, id2, err := b.MintClient(t.Context(), "dup", "test")
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if id1 == "" || id1 == id2 {
		t.Fatalf("ids should be distinct and non-empty: %q, %q", id1, id2)
	}
}

// TestMintValidatesDisplayName: a display_name is a human label, not a key, so
// validation is permissive (spaces/slashes/case allowed) but still rejects empty
// or control-character names that would corrupt the JWT tag or registry JSON.
func TestMintValidatesDisplayName(t *testing.T) {
	b := startTestBus(t)
	for _, bad := range []string{"", "   ", "with\nnewline", "ctrl\x00here"} {
		if _, _, err := b.MintClient(t.Context(), bad, "test"); err == nil {
			t.Errorf("MintClient(%q) should be rejected", bad)
		}
	}
	for _, ok := range []string{"has space", "a/b", "Capitalized", "trail-", ".lead"} {
		if _, _, err := b.MintClient(t.Context(), ok, "test"); err != nil {
			t.Errorf("MintClient(%q) should be accepted: %v", ok, err)
		}
	}
}

func parseJWT(t *testing.T, creds string) string {
	t.Helper()
	j, err := jwt.ParseDecoratedJWT([]byte(creds))
	if err != nil {
		t.Fatalf("parse creds: %v", err)
	}
	return j
}

func TestClientCannotCreateBuckets(t *testing.T) {
	b := startTestBus(t)
	nc, _ := connectClient(t, b, "agent-1")
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	for _, name := range []string{"sx_evil", "clientown"} {
		if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: name}); err == nil {
			t.Errorf("client was allowed to create bucket %q (lifecycle must be denied)", name)
		}
	}
}

// TestIssuedClientIsInDirectory is the positive shape of issuance (ADR-0020): a
// client minted by MintClient (the issuance path) is in the directory the moment
// it is issued, before it ever connects — the record is durable, not presence.
func TestIssuedClientIsInDirectory(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "agent-reg") // mints + persists the record
	var list wireapi.ClientsListOutput
	mustJSON(t, call(t, nc, id, wireapi.OpClientsList, struct{}{}).Result, &list)
	for _, e := range list.Clients {
		if e.ID == id {
			if e.Presence != wireapi.PresenceOnline {
				t.Errorf("a connected client should be online, got %q", e.Presence)
			}
			return
		}
	}
	t.Fatalf("issued client %q absent from directory: %+v", id, list.Clients)
}

// TestRegularClientCannotMint pins the authorization on the issuance path: a
// regular client (a ULID) may not call clients.register to mint identities —
// only the reserved operator/enroll authorities can (ADR-0020). The allow-list
// lets the client *publish* the call under its own prefix; the bus rejects it on
// authorization, so identity creation stays governed by key custody.
func TestRegularClientCannotMint(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "agent-nomint")
	resp := call(t, nc, id, wireapi.OpClientsRegister, wireapi.RegisterInput{DisplayName: "evil", Kind: "worker"})
	if resp.Error == "" {
		t.Fatal("a regular client must not be authorized to mint identities")
	}
	if !strings.Contains(resp.Error, "not authorized") {
		t.Errorf("expected an authorization error, got: %s", resp.Error)
	}
}

func TestClientCannotPublishControl(t *testing.T) {
	b := startTestBus(t)
	errCh := make(chan error, 4)
	nc, _ := connectClient(t, b, "agent-1",
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case errCh <- err:
			default:
			}
		}))
	if err := nc.Publish(sx.SubjectDrain, []byte("x")); err != nil {
		t.Fatalf("publish returned sync error: %v", err)
	}
	_ = nc.Flush()
	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "Permissions Violation") {
			t.Errorf("expected a permissions violation, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected a permissions-violation async error for an sx.control publish")
	}
}

// TestClientCannotSubscribeForeignInbox pins reply confidentiality: a client may
// subscribe only its own per-client inbox (_INBOX.<id>.>), never the shared
// wildcard or another client's inbox — otherwise it could eavesdrop on every
// other client's call replies (the bus replies on the requester's inbox).
func TestClientCannotSubscribeForeignInbox(t *testing.T) {
	b := startTestBus(t)
	errCh := make(chan error, 8)
	nc, _ := connectClient(t, b, "snoop",
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case errCh <- err:
			default:
			}
		}))
	// The shared wildcard and a foreign client's inbox are both outside this
	// client's allow-list (_INBOX.<own-id>.>), so each subscribe is a violation.
	for _, subj := range []string{"_INBOX.>", "_INBOX.someone-else.>"} {
		if _, err := nc.SubscribeSync(subj); err != nil {
			t.Fatalf("subscribe returned a sync error for %q: %v", subj, err)
		}
	}
	_ = nc.Flush()
	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "Permissions Violation") {
			t.Errorf("expected a permissions violation, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected a permissions-violation for subscribing a shared/foreign inbox")
	}
}

func TestDrainDelivers(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "agent-1")
	// Drain is delivered over the client's own push space; subscribe it first.
	sub, err := nc.SubscribeSync(wireapi.DeliverSubject(id, wireapi.DrainSubID))
	if err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	// No register call: Drain targets the clients connected right now, derived from
	// the live connection table (ADR-0020). This client is issued (its record
	// exists) and connected, so it is online and Drain reaches it.
	if err := b.Drain(); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if _, err := sub.NextMsg(2 * time.Second); err != nil {
		t.Errorf("client did not receive the drain delivery: %v", err)
	}
}

// TestHelloHandshake pins the connect handshake (ADR-0020): clients.hello returns
// the bus epoch (so the SDK can hard-gate) for a known identity, and rejects a
// caller with no durable record (never issued, or retired).
func TestHelloHandshake(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "hello-x")

	var out wireapi.HelloOutput
	resp := call(t, nc, id, wireapi.OpClientsHello, wireapi.HelloInput{})
	if resp.Error != "" {
		t.Fatalf("hello: %s", resp.Error)
	}
	mustJSON(t, resp.Result, &out)
	if out.BusEpoch != wire.Epoch {
		t.Errorf("BusEpoch = %d, want %d", out.BusEpoch, wire.Epoch)
	}
	if out.ServerTime == "" {
		t.Error("hello did not stamp a server time")
	}

	// Delete the record out from under the connection (stands in for retire): a
	// subsequent hello must be rejected — the identity is no longer known.
	if err := b.DeleteClientRecord(t.Context(), id); err != nil {
		t.Fatalf("delete record: %v", err)
	}
	if resp := call(t, nc, id, wireapi.OpClientsHello, wireapi.HelloInput{}); resp.Error == "" {
		t.Error("hello for a retired/unknown identity must be rejected")
	}
}

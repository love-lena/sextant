package bus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

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

// connectClient mints a fresh per-client credential for id and connects with it.
func connectClient(t *testing.T, b *Bus, id string, opts ...nats.Option) *nats.Conn {
	t.Helper()
	creds, err := b.MintClient(id)
	if err != nil {
		t.Fatalf("MintClient(%s): %v", id, err)
	}
	path := filepath.Join(t.TempDir(), id+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	all := append([]nats.Option{nats.UserCredentials(path), nats.Name(id)}, opts...)
	nc, err := nats.Connect(b.ClientURL(), all...)
	if err != nil {
		t.Fatalf("connect as %s: %v", id, err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestStartBootstrapsBuckets(t *testing.T) {
	b := startTestBus(t)
	nc := connectClient(t, b, "agent-boot")
	js, err := jetstream.New(nc)
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

// TestPerClientIdentity is the point of JWT auth: distinct clients get distinct,
// verified identities, so ops are attributable.
func TestPerClientIdentity(t *testing.T) {
	b := startTestBus(t)
	alice, err := b.MintClient("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := b.MintClient("bob")
	if err != nil {
		t.Fatal(err)
	}
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
	if ac.Name != "alice" || bc.Name != "bob" {
		t.Errorf("identity names = %q, %q; want alice, bob", ac.Name, bc.Name)
	}
	// Both must actually connect with their own credential.
	_ = connectClient(t, b, "alice")
	_ = connectClient(t, b, "bob")
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
	nc := connectClient(t, b, "agent-1")
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

func TestClientCanWriteConventionBuckets(t *testing.T) {
	b := startTestBus(t)
	nc := connectClient(t, b, "agent-1")
	js, _ := jetstream.New(nc)
	ctx := testCtx(t)
	for _, name := range []string{sx.BucketClients, sx.BucketWorkflows} {
		kv, err := js.KeyValue(ctx, name)
		if err != nil {
			t.Fatalf("client open %s: %v", name, err)
		}
		if _, err := kv.Put(ctx, "agent-1", []byte(`{"ok":true}`)); err != nil {
			t.Errorf("client denied write to convention bucket %s: %v", name, err)
		}
	}
}

func TestClientCannotWriteSystemBucket(t *testing.T) {
	b := startTestBus(t)
	nc := connectClient(t, b, "agent-1")
	js, _ := jetstream.New(nc)
	ctx := testCtx(t)
	kv, err := js.KeyValue(ctx, sx.BucketSystem)
	if err != nil {
		return // denied at the handle level is an acceptable stronger outcome
	}
	if _, err := kv.Put(ctx, "k", []byte(`{}`)); err == nil {
		t.Error("client was allowed to write sx_system")
	}
}

func TestClientCannotPublishControl(t *testing.T) {
	b := startTestBus(t)
	errCh := make(chan error, 4)
	nc := connectClient(t, b, "agent-1",
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

func TestDrainDelivers(t *testing.T) {
	b := startTestBus(t)
	nc := connectClient(t, b, "agent-1")
	sub, err := nc.SubscribeSync(sx.SubjectDrain)
	if err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	if err := b.Drain(); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if _, err := sub.NextMsg(2 * time.Second); err != nil {
		t.Errorf("client did not receive the drain broadcast: %v", err)
	}
}

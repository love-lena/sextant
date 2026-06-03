package bus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func startTestBus(t *testing.T) *Bus {
	t.Helper()
	b, err := Start(context.Background(), Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func connectAs(t *testing.T, b *Bus, user, pass string, opts ...nats.Option) *nats.Conn {
	t.Helper()
	all := append([]nats.Option{nats.UserInfo(user, pass)}, opts...)
	nc, err := nats.Connect(b.ClientURL(), all...)
	if err != nil {
		t.Fatalf("connect as %s: %v", user, err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestStartBootstrapsBuckets(t *testing.T) {
	b := startTestBus(t)
	nc := connectAs(t, b, b.OperatorUser(), b.OperatorPassword())
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

func TestClientCannotCreateBuckets(t *testing.T) {
	b := startTestBus(t)
	nc := connectAs(t, b, b.ClientUser(), b.ClientPassword())
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	// Both a would-be sx_ squat and a client's own bucket: v1 denies all
	// stream/bucket lifecycle to clients (operator provisions buckets).
	for _, name := range []string{"sx_evil", "clientown"} {
		if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: name}); err == nil {
			t.Errorf("client was allowed to create bucket %q (lifecycle must be denied)", name)
		}
	}
}

func TestClientCanWriteConventionBuckets(t *testing.T) {
	b := startTestBus(t)
	nc := connectAs(t, b, b.ClientUser(), b.ClientPassword())
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
	nc := connectAs(t, b, b.ClientUser(), b.ClientPassword())
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
	nc := connectAs(t, b, b.ClientUser(), b.ClientPassword(),
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
	nc := connectAs(t, b, b.ClientUser(), b.ClientPassword())
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

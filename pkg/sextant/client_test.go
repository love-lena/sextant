package sextant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func startBus(t *testing.T) *bus.Bus {
	t.Helper()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

// credsPath mints a fresh per-client credential for id and writes it to a temp
// file, returning the path. Each client gets its own verified identity.
func credsPath(t *testing.T, b *bus.Bus, id string) string {
	t.Helper()
	creds, _, err := b.MintClient(id)
	if err != nil {
		t.Fatalf("MintClient(%s): %v", id, err)
	}
	path := filepath.Join(t.TempDir(), id+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func dialClient(t *testing.T, b *bus.Bus, id string) *Client {
	t.Helper()
	c, err := Connect(t.Context(), Options{
		URL:       b.ClientURL(),
		CredsPath: credsPath(t, b, id),
		Logf:      func(string, ...any) {}, // quiet in tests
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", id, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// inspectJS connects a throwaway client (a raw connection that does not
// register) and returns its JetStream handle, for reading the registry —
// clients may read sx_clients.
func inspectJS(t *testing.T, b *bus.Bus) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(b.ClientURL(), nats.UserCredentials(credsPath(t, b, "inspector")))
	if err != nil {
		t.Fatalf("inspector connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func readCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestConnectRegisters also pins the identity contract: the client's id is the
// bus-minted ULID in its credential (its registry key), and its display_name is
// the human label minted with it.
func TestConnectRegisters(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "agent-reg")
	if c.ID() == "" {
		t.Fatal("ID() is empty; want the credential's bus-minted ULID")
	}
	if c.DisplayName() != "agent-reg" {
		t.Fatalf("DisplayName() = %q; want agent-reg", c.DisplayName())
	}
	clients, err := inspectJS(t, b).KeyValue(readCtx(t), sx.BucketClients)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clients.Get(readCtx(t), c.ID()); err != nil {
		t.Errorf("client not found in registry: %v", err)
	}
}

func TestConnectRequiresCreds(t *testing.T) {
	b := startBus(t)
	_, err := Connect(t.Context(), Options{URL: b.ClientURL(), Logf: func(string, ...any) {}})
	if err == nil {
		t.Fatal("expected connect to fail without credentials")
	}
}

func TestConnectViaConnInfoFile(t *testing.T) {
	b := startBus(t)
	path := filepath.Join(t.TempDir(), conninfo.DefaultFile)
	if err := conninfo.Write(path, conninfo.Info{URL: b.ClientURL()}); err != nil {
		t.Fatal(err)
	}
	c, err := Connect(t.Context(), Options{
		ConnInfoPath: path,
		CredsPath:    credsPath(t, b, "via-conninfo"),
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect via conn info: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
}

func TestEpochMismatchFailsLoud(t *testing.T) {
	b := startBus(t)
	// Rewrite the bus epoch to something the client won't match. (A client can
	// write sx_meta today — per-row write-precision is the deferred refinement,
	// ADR-0012; here it's just the simplest way to force a mismatch.)
	meta, err := inspectJS(t, b).KeyValue(readCtx(t), sx.BucketMeta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := meta.Put(readCtx(t), sx.MetaKeyEpoch, []byte("999")); err != nil {
		t.Fatal(err)
	}
	_, err = Connect(t.Context(), Options{
		URL:       b.ClientURL(),
		CredsPath: credsPath(t, b, "agent-epoch"),
		Logf:      func(string, ...any) {},
	})
	if err == nil {
		t.Fatal("expected connect to fail loud on epoch mismatch")
	}
	ee, ok := errors.AsType[*wire.EpochError](err)
	if !ok {
		t.Fatalf("expected a *wire.EpochError, got: %v", err)
	}
	if ee.Got != wire.Epoch || ee.Want != 999 {
		t.Errorf("epoch error fields = %+v", ee)
	}
}

func TestDrainClosesDrained(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "agent-drain")
	if err := b.Drain(); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	select {
	case <-c.Drained():
	case <-time.After(3 * time.Second):
		t.Fatal("Drained() did not close after a drain broadcast")
	}
}

func TestCloseLeavesRegistry(t *testing.T) {
	b := startBus(t)
	c, err := Connect(t.Context(), Options{
		URL:       b.ClientURL(),
		CredsPath: credsPath(t, b, "agent-leave"),
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	clients, err := inspectJS(t, b).KeyValue(readCtx(t), sx.BucketClients)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clients.Get(readCtx(t), id); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Errorf("registry record should be gone after Close; got err=%v", err)
	}
}

func TestClockSkewHelper(t *testing.T) {
	base := time.Now()
	if got := clockSkew(base.Add(2*time.Second), base); got != 2*time.Second {
		t.Errorf("clockSkew = %v, want 2s", got)
	}
	if got := clockSkew(base, base.Add(3*time.Second)); got.Abs() != 3*time.Second {
		t.Errorf("clockSkew abs = %v, want 3s", got.Abs())
	}
}

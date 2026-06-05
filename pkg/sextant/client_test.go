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
	"github.com/love-lena/sextant/pkg/wire"
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
	// Confirm the registry write through the SDK's own read path — under the
	// allow-list a client has no direct registry access, so clients.list is how
	// it learns it is registered.
	got, err := c.ListClients(readCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ci := range got {
		if ci.ID == c.ID() {
			found = true
		}
	}
	if !found {
		t.Errorf("client %q not found in registry: %+v", c.ID(), got)
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
	// Move the bus epoch to something the client won't match. Under the allow-list
	// a client cannot write sx_meta — only the bus can — so this is an operator
	// write seam, which is also the honest shape of an epoch bump in production.
	if err := b.SetEpoch(readCtx(t), 999); err != nil {
		t.Fatal(err)
	}
	_, err := Connect(t.Context(), Options{
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
	// An observer stays connected to read the directory after the leaver closes:
	// the leaver's own connection is gone, so it can no longer list itself.
	observer := dialClient(t, b, "observer")
	leaver, err := Connect(t.Context(), Options{
		URL:       b.ClientURL(),
		CredsPath: credsPath(t, b, "agent-leave"),
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := leaver.ID()
	if err := leaver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := observer.ListClients(readCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, ci := range got {
		if ci.ID == id {
			t.Errorf("registry record should be gone after Close, still listed: %+v", ci)
		}
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

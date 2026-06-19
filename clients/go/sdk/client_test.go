package sextant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/protocol/conninfo"
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
	creds, _, err := b.MintClient(t.Context(), id, "test")
	if err != nil {
		t.Fatalf("MintClient(%s): %v", id, err)
	}
	return writeCreds(t, creds)
}

// writeCreds writes credential text to a temp file and returns its path.
func writeCreds(t *testing.T, creds string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "creds")
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

// presenceOf returns a connected client's presence in the directory, or ("",
// false) if it is absent.
func presenceOf(t *testing.T, c *Client, id string) (online, present bool) {
	t.Helper()
	got, err := c.ListClients(readCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, ci := range got {
		if ci.ID == id {
			return ci.Online, true
		}
	}
	return false, false
}

// waitPresence polls the directory (via reader) until id reaches the wanted
// online state, or fails after a short timeout. Presence is connection-derived,
// so it is self-correcting but not instantaneous after a disconnect.
func waitPresence(t *testing.T, reader *Client, id string, wantOnline bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if online, present := presenceOf(t, reader, id); present && online == wantOnline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	online, present := presenceOf(t, reader, id)
	t.Fatalf("client %q presence did not reach online=%v (present=%v online=%v)", id, wantOnline, present, online)
}

// TestConnectAndDirectory pins the identity contract and connection-derived
// presence: the client's id is the bus-minted ULID in its credential, its
// display_name is the human label minted with it, and a connected client shows up
// online in the directory (ADR-0020).
func TestConnectAndDirectory(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "agent-reg")
	if c.ID() == "" {
		t.Fatal("ID() is empty; want the credential's bus-minted ULID")
	}
	if c.DisplayName() != "agent-reg" {
		t.Fatalf("DisplayName() = %q; want agent-reg", c.DisplayName())
	}
	if online, present := presenceOf(t, c, c.ID()); !present || !online {
		t.Errorf("connected client %q should be present and online (present=%v online=%v)", c.ID(), present, online)
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

// TestCloseGoesOffline pins ADR-0020: a clean Close drops presence to offline but
// does NOT retire — the durable identity persists in the directory, so the same
// client can reconnect later. (Decommissioning for good is an explicit operator
// retire, never a consequence of Close.)
func TestCloseGoesOffline(t *testing.T) {
	b := startBus(t)
	// An observer stays connected to read the directory after the leaver closes.
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
	waitPresence(t, observer, id, true) // online while connected
	if err := leaver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitPresence(t, observer, id, false) // offline after a clean close...
	if _, present := presenceOf(t, observer, id); !present {
		t.Errorf("identity %q should persist in the directory after Close (offline, not gone)", id)
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

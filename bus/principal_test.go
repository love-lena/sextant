package bus_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/wireapi"
)

// Principal designation (ADR-0030, TASK-54): the bus records its one principal in
// a client-readable, Operator-writable sx key (sx_meta/principal). These tests
// drive the real SDK (Client + the operator Issuer) against a real bus, so they
// exercise the same paths production does: discover-on-connect, the operator-only
// write gate, and the watch that lets a connected client observe a change.

// startBusAt starts a bus on a known store dir (so a test can reach the operator
// credential the bus provisions there) and writes the discovery file, the way
// `sextant up` does. It returns the bus and the store dir.
func startBusAt(t *testing.T) (*bus.Bus, string) {
	t.Helper()
	store := t.TempDir()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: b.ClientURL()}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}
	return b, store
}

// connectOperator dials an Issuer on the bus with the operator credential the bus
// provisioned in store — the held-identity authority that may set the principal.
func connectOperator(t *testing.T, b *bus.Bus, store string) *sextant.Issuer {
	t.Helper()
	iss, err := sextant.ConnectIssuer(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: bus.OperatorCredsPath(store),
	})
	if err != nil {
		t.Fatalf("ConnectIssuer (operator): %v", err)
	}
	t.Cleanup(func() { _ = iss.Close() })
	return iss
}

// TestPrincipalBootstrapDefault (AC#3): bus bootstrap defaults the principal to
// the operator's seat. At bootstrap no human client ULID exists yet — the seat
// that exists is the reserved operator identity — so the default is "operator".
func TestPrincipalBootstrapDefault(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "discoverer")
	if got := c.Principal(); got != wireapi.OperatorID {
		t.Fatalf("bootstrap principal = %q, want %q (the operator's seat)", got, wireapi.OperatorID)
	}
}

// TestPrincipalDiscoveredOnConnect (AC#4, first half): a connecting client learns
// the current principal in the hello handshake, before any explicit read.
func TestPrincipalDiscoveredOnConnect(t *testing.T) {
	b, store := startBusAt(t)
	iss := connectOperator(t, b, store)
	target := "01HZZZCLIENTULIDXXXXXXXXXX"
	if err := iss.SetPrincipal(t.Context(), target, false); err != nil {
		t.Fatalf("SetPrincipal: %v", err)
	}
	// A client that connects AFTER the set discovers the new value at hello.
	c := dialClient(t, b, "late-joiner")
	if got := c.Principal(); got != target {
		t.Fatalf("discovered principal = %q, want %q", got, target)
	}
	got, err := c.GetPrincipal(t.Context())
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	if got != target {
		t.Fatalf("GetPrincipal = %q, want %q", got, target)
	}
}

// TestPrincipalSetGetRoundTrip (AC#2): the operator sets/re-points the principal
// and a read returns the new value.
func TestPrincipalSetGetRoundTrip(t *testing.T) {
	b, store := startBusAt(t)
	iss := connectOperator(t, b, store)

	first := "01HZZZFIRSTPRINCIPALXXXXXX"
	if err := iss.SetPrincipal(t.Context(), first, false); err != nil {
		t.Fatalf("SetPrincipal(first): %v", err)
	}
	if got, err := iss.GetPrincipal(t.Context()); err != nil || got != first {
		t.Fatalf("GetPrincipal after first set = %q, %v; want %q", got, err, first)
	}

	// Re-point (the two-way door) — deliberate, so it takes --force (ADR-0031).
	second := "01HZZZSECONDPRINCIPALXXXXX"
	if err := iss.SetPrincipal(t.Context(), second, false); err == nil {
		t.Fatal("re-pointing an established principal without force must be refused")
	}
	if err := iss.SetPrincipal(t.Context(), second, true); err != nil {
		t.Fatalf("SetPrincipal(second, force): %v", err)
	}
	if got, err := iss.GetPrincipal(t.Context()); err != nil || got != second {
		t.Fatalf("GetPrincipal after re-point = %q, %v; want %q", got, err, second)
	}
}

// TestPrincipalClientCanRead (AC#1, read half): a client-tier credential can READ
// the principal. The write half — that a client-tier set is DENIED by the bus
// (AC#5) — is proven in the internal-package test
// TestPrincipalSetIsOperatorOnly, which calls the operation raw under a client's
// own subject token to reach the bus's gate directly.
func TestPrincipalClientCanRead(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "client-tier")
	got, err := c.GetPrincipal(t.Context())
	if err != nil {
		t.Fatalf("a client must be able to READ the principal: %v", err)
	}
	if got != wireapi.OperatorID {
		t.Fatalf("client read principal = %q, want the bootstrap default %q", got, wireapi.OperatorID)
	}
}

// TestPrincipalWatchObservesChange (AC#4, second half): a connected client
// observes a re-designation through principal.watch — no reconnect — and its
// cached Principal() advances to match.
func TestPrincipalWatchObservesChange(t *testing.T) {
	b, store := startBusAt(t)
	c := dialClient(t, b, "watcher")

	changes := make(chan string, 8)
	w, err := c.WatchPrincipal(t.Context(), func(p string) { changes <- p })
	if err != nil {
		t.Fatalf("WatchPrincipal: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	// First delivery is the current value (the bootstrap default).
	if got := recvWithin(t, changes, 5*time.Second); got != wireapi.OperatorID {
		t.Fatalf("first watch delivery = %q, want the current value %q", got, wireapi.OperatorID)
	}

	// The operator re-points; the watcher observes it without reconnecting.
	iss := connectOperator(t, b, store)
	target := "01HZZZWATCHEDPRINCIPALXXXX"
	if err := iss.SetPrincipal(t.Context(), target, false); err != nil {
		t.Fatalf("SetPrincipal: %v", err)
	}
	if got := recvWithin(t, changes, 5*time.Second); got != target {
		t.Fatalf("watch delivery after re-point = %q, want %q", got, target)
	}
	// The cached accessor advanced too (what TASK-56's auth hook reads).
	deadline := time.Now().Add(2 * time.Second)
	for c.Principal() != target && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := c.Principal(); got != target {
		t.Fatalf("cached Principal() = %q, want %q after a watched change", got, target)
	}
}

func recvWithin(t *testing.T, ch <-chan string, d time.Duration) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		t.Fatalf("no value within %s", d)
		return ""
	}
}

package bus

import (
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/conninfo"
)

// TestRestartKeepsPort: a bus started against a store, stopped, then restarted
// against the same store binds the same port — so any enrolled context whose
// URL was pinned at the first start remains valid after the restart.
func TestRestartKeepsPort(t *testing.T) {
	store := t.TempDir()

	// First start — ephemeral port (fresh store, no bus.json yet).
	b1, err := Start(t.Context(), Config{StoreDir: store})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	firstURL := b1.ClientURL()
	firstPort := mustParsePort(t, firstURL)

	// Write the discovery file exactly as cmdUp does, so the restart reads it.
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: firstURL}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	// Hard stop — port must be free before the restart (the check verifies this).
	b1.Shutdown()
	// Give the OS a moment to release the port.
	deadline := time.Now().Add(3 * time.Second)
	for {
		ln, err := net.Listen("tcp", "127.0.0.1:"+firstPort)
		if err == nil {
			_ = ln.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("port was not released within 3s of Shutdown — cannot test restart")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Second start against the same store — must reclaim firstPort.
	b2, err := Start(t.Context(), Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)

	secondURL := b2.ClientURL()
	secondPort := mustParsePort(t, secondURL)

	if firstPort != secondPort {
		t.Errorf("restart rotated port: first=%s second=%s (enrolled contexts would go stale)", firstPort, secondPort)
	}
}

// TestRestartFallsBackWhenPortTaken: when the recorded port is occupied,
// Start falls back to a fresh ephemeral port — the bus always comes up — and
// the new URL is recorded in bus.json so a subsequent clean restart is stable
// again. The fallback notice arrives through Config.Logf (the library's only
// output channel), and a nil Logf still works (the stderr default).
func TestRestartFallsBackWhenPortTaken(t *testing.T) {
	store := t.TempDir()

	// First start — pick an ephemeral port. A clean start with a capturing
	// Logf must log nothing.
	rec1 := &logRecorder{}
	b1, err := Start(t.Context(), Config{StoreDir: store, Logf: rec1.logf})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	firstURL := b1.ClientURL()
	firstPort := mustParsePort(t, firstURL)

	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: firstURL}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}
	b1.Shutdown()
	if lines := rec1.all(); len(lines) != 0 {
		t.Errorf("clean start+stop logged unexpectedly: %q", lines)
	}

	// Occupy the recorded port with a plain listener so the restart cannot bind it.
	squatter, err := net.Listen("tcp", "127.0.0.1:"+firstPort)
	if err != nil {
		t.Fatalf("could not occupy port %s (race with OS release): %v", firstPort, err)
	}
	t.Cleanup(func() { _ = squatter.Close() })

	// Second start: must succeed on a different port (fallback, not an error),
	// and the notice must arrive through the Logf hook.
	rec2 := &logRecorder{}
	b2, err := Start(t.Context(), Config{StoreDir: store, Logf: rec2.logf})
	if err != nil {
		t.Fatalf("Start with occupied port should fall back, not error: %v", err)
	}

	secondURL := b2.ClientURL()
	secondPort := mustParsePort(t, secondURL)

	if firstPort == secondPort {
		t.Errorf("bus bound the squatted port %s — expected a different fallback port", firstPort)
	}
	var sawNotice bool
	for _, line := range rec2.all() {
		// The notice must be LOUD about a RANDOM rebind so clients pinned to the
		// recorded port know to re-resolve (the v0.5.1 outage).
		if strings.Contains(line, "recorded port "+firstPort+" unavailable") && strings.Contains(line, "RANDOM") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Errorf("loud port-fallback notice did not arrive through Config.Logf: %q", rec2.all())
	}
	b2.Shutdown()

	// Third start, still squatted, with a nil Logf: the default path (one line
	// to stderr) is exercised — no panic, and the bus still comes up.
	// (bus.json still records the squatted firstPort; only cmdUp rewrites it.)
	b3, err := Start(t.Context(), Config{StoreDir: store})
	if err != nil {
		t.Fatalf("Start with nil Logf should use the stderr default, not fail: %v", err)
	}
	t.Cleanup(b3.Shutdown)
}

// TestExplicitPortBindsDeterministically: a non-zero Config.Port is a pin — the
// bus binds exactly that port (no recorded-port reuse, no random fallback).
func TestExplicitPortBindsDeterministically(t *testing.T) {
	// Pick a free port to pin, then release it so the bus can claim it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	want := mustParsePort(t, "nats://"+ln.Addr().String())
	_ = ln.Close()

	wantN, _ := strconv.Atoi(want)
	b, err := Start(t.Context(), Config{StoreDir: t.TempDir(), Port: wantN})
	if err != nil {
		t.Fatalf("Start with explicit port %d: %v", wantN, err)
	}
	t.Cleanup(b.Shutdown)
	if got := mustParsePort(t, b.ClientURL()); got != want {
		t.Errorf("explicit port not honored: want %s, got %s", want, got)
	}
}

// TestExplicitPortFailsLoudWhenTaken: a pinned port that is unavailable must
// FAIL LOUD (an error naming the port), never silently fall back to random —
// the operator who pinned a port gets that port or a clear reason why not.
func TestExplicitPortFailsLoudWhenTaken(t *testing.T) {
	// Occupy a port, then ask the bus to pin it.
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	t.Cleanup(func() { _ = squatter.Close() })
	taken := mustParsePort(t, "nats://"+squatter.Addr().String())
	takenN, _ := strconv.Atoi(taken)

	_, err = Start(t.Context(), Config{StoreDir: t.TempDir(), Port: takenN})
	if err == nil {
		t.Fatalf("Start with a taken explicit port should fail loud, not fall back")
	}
	if !strings.Contains(err.Error(), taken) {
		t.Errorf("explicit-port failure should name the port %s, got: %v", taken, err)
	}
}

// TestFreshStoreBoot: a fresh store (no bus.json) starts without error and
// picks any available port — the base case is unchanged.
func TestFreshStoreBoot(t *testing.T) {
	b, err := Start(t.Context(), Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("fresh store Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	if b.ClientURL() == "" {
		t.Error("fresh-store start produced an empty ClientURL")
	}
}

// mustParsePort parses the port string out of a nats://host:port URL.
func mustParsePort(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse bus URL %q: %v", rawURL, err)
	}
	p := u.Port()
	if p == "" {
		t.Fatalf("bus URL %q has no port", rawURL)
	}
	return p
}

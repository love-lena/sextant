package bus_test

// Prod-faithful regression for the v0.5.1 bus-restart incident: when the bus
// restarts on a DIFFERENT port (address-stability is best-effort and can fall
// back to a new port — e.g. the old listener wasn't free at boot), a live client
// must FOLLOW to the new port by re-resolving bus.json on reconnect, not hammer
// the dead boot port forever (which stranded every client in the incident).
//
// This drives the full SDK Connect path via DISCOVERY (Options.ConnInfoPath), so
// the re-resolve dialer is in play exactly as in production. It deliberately does
// NOT pin Options.URL — a pinned URL is the old, port-blind behaviour and (by
// design) would not follow. The seam the prior restart tests missed: they reused
// the SAME port (ADR-0025 happy path), so they never exercised a port change.

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

func TestSubscriptionFollowsBusPortChange(t *testing.T) {
	store := t.TempDir()
	busJSON := filepath.Join(store, conninfo.DefaultFile)

	b1 := startBusWithStore(t, store) // also writes bus.json with b1's port
	creds := mintCredsFile(t, b1, store, "port-follower")
	portA := portOf(t, b1.ClientURL())

	// Connect via DISCOVERY (not a pinned URL) so the production re-resolve dialer
	// is attached.
	c, reconnected := dialViaDiscoveryWithReconnectSignal(t, busJSON, creds)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("portchange")
	got := make(chan sextant.Message, 8)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	// Sanity: live delivery works on the first bus.
	mustPublish(t, c, subj, json.RawMessage(`{"phase":"before"}`))
	waitMsg(t, got, "pre-restart delivery")

	// Restart the bus on a DIFFERENT port — the incident.
	hardStopAndWait(t, b1)
	portB := freePortOtherThan(t, portA)
	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store, Port: portB})
	if err != nil {
		t.Fatalf("restart bus on new port %d: %v", portB, err)
	}
	t.Cleanup(b2.Shutdown)
	if same := portOf(t, b2.ClientURL()); same == portA {
		t.Fatalf("precondition: bus restarted on the same port %s; this test needs a port change", same)
	}
	// Update discovery to the new port — what `sextant up` writes on boot.
	if err := conninfo.Write(busJSON, conninfo.Info{URL: b2.ClientURL()}); err != nil {
		t.Fatalf("rewrite bus.json: %v", err)
	}

	// The client must FOLLOW to the new port via the re-resolve dialer. Without
	// the fix it dials the dead portA forever and this times out.
	select {
	case <-reconnected:
	case <-time.After(30 * time.Second):
		t.Fatal("client did not reconnect after the bus moved ports — SDK is not re-resolving bus.json on reconnect")
	}

	// Delivery resumes on the NEW port.
	mustPublish(t, c, subj, json.RawMessage(`{"phase":"after"}`))
	waitMsg(t, got, "post-port-change delivery")
}

// dialViaDiscoveryWithReconnectSignal connects via Options.ConnInfoPath (not a
// pinned URL) so the re-resolve dialer is active, and signals once per completed
// reconnect+resume pass (the "reconnected to the bus" log — see startResumePass).
func dialViaDiscoveryWithReconnectSignal(t *testing.T, connInfoPath, credsPath string) (*sextant.Client, <-chan struct{}) {
	t.Helper()
	reconnected := make(chan struct{}, 4)
	c, err := sextant.Connect(t.Context(), sextant.Options{
		ConnInfoPath: connInfoPath,
		CredsPath:    credsPath,
		Logf: func(format string, args ...any) {
			if strings.Contains(fmt.Sprintf(format, args...), "reconnected to the bus") {
				select {
				case reconnected <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect via discovery: %v", err)
	}
	return c, reconnected
}

// freePortOtherThan returns a free loopback TCP port whose number differs from
// `not`, so the restart is guaranteed to change the port.
func freePortOtherThan(t *testing.T, not string) int {
	t.Helper()
	for range 30 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen :0: %v", err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if strconv.Itoa(p) != not {
			return p
		}
	}
	t.Fatal("could not find a free port different from the original")
	return 0
}

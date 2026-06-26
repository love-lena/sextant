//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

// TestClientAutoDMOnConnect is the TASK-55 definition-of-done: a freshly-connected
// client receives a direct message on msg.client.<self> without any explicit
// Subscribe call (AC#1, AC#2). It drives the built `sextant` binary to register
// two clients, then uses in-process SDK connections to assert delivery:
//
//   - AC#1: receiver.Inbox() delivers without an explicit Subscribe.
//   - AC#2: a message published to a fresh client's DM is delivered to it.
func TestClientAutoDMOnConnect(t *testing.T) {
	h := newHarness(t)
	h.startBus()

	// Register two clients via the CLI (identical to how other e2e tests do it).
	receiverOut, code := h.run(nil, "clients", "register", "dm-receiver", "--kind", "worker", "--store", h.store)
	if code != 0 {
		t.Fatalf("register dm-receiver exited %d: %s", code, receiverOut)
	}
	receiverID := mustParseID(t, receiverOut, `registered dm-receiver as (`+ulidPat+`)`)
	receiverCreds := filepath.Join(h.store, "dm-receiver.creds")

	senderOut, code := h.run(nil, "clients", "register", "dm-sender", "--kind", "worker", "--store", h.store)
	if code != 0 {
		t.Fatalf("register dm-sender exited %d: %s", code, senderOut)
	}
	senderCreds := filepath.Join(h.store, "dm-sender.creds")

	bURL := busURL(t, h.store)

	// Connect the receiver — no explicit Subscribe call at any point.
	receiver, err := sextant.Connect(context.Background(), sextant.Options{
		URL:       bURL,
		CredsPath: receiverCreds,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("connect receiver: %v", err)
	}
	defer receiver.Close()

	// AC#1: the client must already be subscribed to its own DM subject.
	if receiver.ID() != receiverID {
		t.Fatalf("receiver ID = %q, want %q", receiver.ID(), receiverID)
	}
	dmSubject := sx.ClientSubject(receiverID)

	// Connect the sender.
	sender, err := sextant.Connect(context.Background(), sextant.Options{
		URL:       bURL,
		CredsPath: senderCreds,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("connect sender: %v", err)
	}
	defer sender.Close()

	// AC#2: publish to the receiver's DM subject; it must arrive on receiver.Inbox()
	// with no explicit Subscribe by the receiver.
	payload := json.RawMessage(`{"task":"do the thing"}`)
	if err := sender.Publish(context.Background(), dmSubject, payload); err != nil {
		t.Fatalf("Publish DM: %v", err)
	}

	select {
	case m := <-receiver.Inbox():
		if m.Subject != dmSubject {
			t.Errorf("DM subject = %q, want %q", m.Subject, dmSubject)
		}
		if m.Frame.Author != sender.ID() {
			t.Errorf("DM author = %q, want sender %q", m.Frame.Author, sender.ID())
		}
		if string(m.Frame.Record) != string(payload) {
			t.Errorf("DM record = %s, want %s", m.Frame.Record, payload)
		}
		t.Logf("AC#1+AC#2 pass: DM delivered on %s without explicit Subscribe", dmSubject)
	case <-time.After(stepTimeout):
		t.Fatalf("no DM delivery within %s: auto-subscription may not be established", stepTimeout)
	}
}

// TestClientAutoDMSurvivesReconnect is the TASK-55 AC#3 definition-of-done:
// the auto-DM subscription survives the normal connect/resume (reconnect) path.
// It starts a bus in-process (for controlled shutdown/restart), connects two SDK
// clients, publishes a DM, verifies delivery, then restarts the bus and verifies
// a second DM arrives after the reconnect — confirming the auto-subscription's
// relay was re-established (ADR-0027).
func TestClientAutoDMSurvivesReconnect(t *testing.T) {
	store := t.TempDir()

	// Start the bus in-process so we can shut it down and restart it
	// deterministically (mirroring TestResumeTransportFailureDefersUntilNextReconnect
	// in pkg/sextant).
	b1, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	// Write bus.json so the same port is reclaimed on restart.
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: b1.ClientURL()}); err != nil {
		t.Fatalf("write bus.json: %v", err)
	}
	t.Cleanup(b1.Shutdown)

	// Mint credentials before the first shutdown (they are durable in the store).
	receiverCreds, _, err := b1.MintClient(t.Context(), "dm-recv-reconnect", "test")
	if err != nil {
		t.Fatalf("MintClient receiver: %v", err)
	}
	senderCreds, _, err := b1.MintClient(t.Context(), "dm-send-reconnect", "test")
	if err != nil {
		t.Fatalf("MintClient sender: %v", err)
	}

	receiverCredsFile := writeTempCreds(t, receiverCreds)
	senderCredsFile := writeTempCreds(t, senderCreds)

	// reconnected fires when the receiver logs "reconnected to the bus".
	reconnected := make(chan struct{}, 4)

	receiver, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b1.ClientURL(),
		CredsPath: receiverCredsFile,
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
		t.Fatalf("connect receiver: %v", err)
	}
	defer receiver.Close()

	sender, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b1.ClientURL(),
		CredsPath: senderCredsFile,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("connect sender: %v", err)
	}
	defer sender.Close()

	dmSubject := sx.ClientSubject(receiver.ID())

	// Pre-restart: first DM delivery (proves initial auto-subscription works).
	if err := sender.Publish(t.Context(), dmSubject, json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatalf("pre-restart Publish: %v", err)
	}
	select {
	case m := <-receiver.Inbox():
		if string(m.Frame.Record) != `{"n":1}` {
			t.Fatalf("pre-restart DM = %s, want {\"n\":1}", m.Frame.Record)
		}
	case <-time.After(stepTimeout):
		t.Fatal("pre-restart DM not delivered")
	}

	// Restart the bus: shut it down, free the port, bring it back on the same store.
	busURL := b1.ClientURL()
	b1.Shutdown()
	t.Cleanup(func() {}) // already shut down; prevent double-shutdown

	u, err := url.Parse(busURL)
	if err != nil {
		t.Fatalf("parse bus URL: %v", err)
	}
	// Wait for the port to be free before restarting.
	deadline := time.Now().Add(5 * time.Second)
	for {
		ln, lerr := net.Listen("tcp", "127.0.0.1:"+u.Port())
		if lerr == nil {
			_ = ln.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("port was not released within 5s of Shutdown — cannot restart")
		}
		time.Sleep(20 * time.Millisecond)
	}

	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second bus.Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)

	// Wait for the receiver to reconnect (its resume pass re-establishes the
	// auto-DM subscription relay).
	select {
	case <-reconnected:
		t.Log("receiver reconnected to the restarted bus")
	case <-time.After(20 * time.Second):
		t.Fatal("receiver did not reconnect within 20s")
	}

	// AC#3: post-restart DM must still be delivered via the auto-subscription.
	if err := sender.Publish(t.Context(), dmSubject, json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatalf("post-restart Publish: %v", err)
	}
	select {
	case m := <-receiver.Inbox():
		if string(m.Frame.Record) != `{"n":2}` {
			t.Fatalf("post-restart DM = %s, want {\"n\":2} (no duplicate replay of n=1)", m.Frame.Record)
		}
		t.Log("AC#3 pass: auto-DM subscription survived reconnect")
	case <-time.After(stepTimeout):
		t.Fatal("post-restart DM not delivered: auto-subscription relay was not re-established")
	}

	// No stale duplicate of n=1.
	select {
	case m := <-receiver.Inbox():
		t.Fatalf("unexpected duplicate DM after reconnect: %s", m.Frame.Record)
	case <-time.After(500 * time.Millisecond):
	}
}

// writeTempCreds writes NATS credential text to a temp file and returns its path.
func writeTempCreds(t *testing.T, creds string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.creds")
	if err != nil {
		t.Fatalf("create temp creds: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(creds); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	return f.Name()
}

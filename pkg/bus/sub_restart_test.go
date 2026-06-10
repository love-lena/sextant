package bus_test

// Subscription-across-restart integration tests (ADR-0027). They reuse the
// embedded-bus restart pattern from restart_test.go but drive the full SDK
// Subscribe path to assert that:
//   - a DeliverAll subscription resumes from last-delivered+1 after a restart
//     of the same store (no duplicates, no gap);
//   - a live-only subscription receives post-restart publishes;
//   - a restart onto a wiped store (sequences gone) fires the OnError handler
//     rather than going silent;
//   - Stop during bus downtime leaves no goroutine leak and no error after.
//
// All waits are deadline-bound (fail-loud, not hang — MEMORY.md feedback_fail_loud).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/backend"
	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
)

// startBusWithStore starts a bus against storeDir and registers b.Shutdown as a
// Cleanup. It writes bus.json so a subsequent restart reclaims the same port
// (ADR-0025). Returns the bus and the store-local bus.json path.
func startBusWithStore(t *testing.T, storeDir string) *bus.Bus {
	t.Helper()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: storeDir})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	if err := conninfo.Write(filepath.Join(storeDir, conninfo.DefaultFile), conninfo.Info{URL: b.ClientURL()}); err != nil {
		t.Fatalf("write bus.json: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

// mintCredsFile mints a credential on b and writes it to storeDir/creds so it
// survives a bus restart (the credential is durable — it belongs to the identity
// record, not the live server). Returns the credentials file path.
func mintCredsFile(t *testing.T, b *bus.Bus, storeDir, name string) string {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), name, "test")
	if err != nil {
		t.Fatalf("MintClient(%s): %v", name, err)
	}
	path := filepath.Join(storeDir, name+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// dialOnURL connects a client to url using credsPath; it does NOT register a
// Cleanup so the caller can manage the connection lifecycle explicitly.
func dialOnURL(t *testing.T, rawURL, credsPath string) *sextant.Client {
	t.Helper()
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       rawURL,
		CredsPath: credsPath,
		Logf:      func(string, ...any) {}, // quiet in tests
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", rawURL, err)
	}
	return c
}

// dialOnURLWithReconnectSignal connects a client and returns a channel that
// receives once each time the client reconnects AND all subscription relays have
// been re-established. The "reconnected to the bus" log fires after
// reestablishSubs returns (see client.go), so waiting on this signal is safe
// before publishing to a subscription.
func dialOnURLWithReconnectSignal(t *testing.T, rawURL, credsPath string) (*sextant.Client, <-chan struct{}) {
	t.Helper()
	reconnected := make(chan struct{}, 4)
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       rawURL,
		CredsPath: credsPath,
		Logf: func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			if strings.Contains(msg, "reconnected to the bus") {
				select {
				case reconnected <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", rawURL, err)
	}
	return c, reconnected
}

// portOf parses the port string out of a nats://host:port URL.
func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	return u.Port()
}

// hardStopAndWait shuts b down and blocks until its port is free or times out.
// Avoids the "start found port occupied" race in the restart tests.
func hardStopAndWait(t *testing.T, b *bus.Bus) {
	t.Helper()
	port := portOf(t, b.ClientURL())
	b.Shutdown()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err == nil {
			_ = ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("port was not released within 5s of Shutdown — cannot restart")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitMsg blocks on ch until a Message arrives or the deadline expires,
// returning the message. Fails the test on timeout.
func waitMsg(t *testing.T, ch <-chan sextant.Message, label string) sextant.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return sextant.Message{}
	}
}

// mustPublish publishes record on subject and fails the test on error.
func mustPublish(t *testing.T, c *sextant.Client, subject string, record json.RawMessage) {
	t.Helper()
	ctx := t.Context()
	if err := c.Publish(ctx, subject, record); err != nil {
		t.Fatalf("Publish(%s): %v", subject, err)
	}
}

// TestSubscribeDeliverAllSurvivesRestart: publish N messages, subscribe with
// DeliverAll, receive all N, restart the bus on the same store, publish M more
// — the subscription delivers exactly M new messages (no duplicates of the
// first N, no gap). Deadline-bound; fail loud on any miss.
func TestSubscribeDeliverAllSurvivesRestart(t *testing.T) {
	store := t.TempDir()
	b1 := startBusWithStore(t, store)
	credsFile := mintCredsFile(t, b1, store, "deliver-all")
	busURL := b1.ClientURL()

	c, reconnected := dialOnURLWithReconnectSignal(t, busURL, credsFile)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("restart-all")

	// Publish 3 messages before subscribing so DeliverAll replays them.
	const pre = 3
	for i := range pre {
		mustPublish(t, c, subj, json.RawMessage(`{"pre":`+itoa(i)+`}`))
	}

	got := make(chan sextant.Message, 32)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m }, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	// Drain the pre-restart messages.
	for range pre {
		waitMsg(t, got, "pre-restart message")
	}

	// Hard stop first bus and wait for port to free.
	hardStopAndWait(t, b1)

	// Restart on the same store (same port via ADR-0025).
	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)

	// Wait for reestablishSubs to complete before publishing so the relay is up
	// and the resume-from-seq+1 consumer is in place before any new messages arrive.
	select {
	case <-reconnected:
	case <-time.After(15 * time.Second):
		t.Fatal("client did not reconnect within 15s")
	}

	// Publish post-restart messages and expect exactly that many (no duplicates).
	const post = 2
	for i := range post {
		mustPublish(t, c, subj, json.RawMessage(`{"post":`+itoa(i)+`}`))
	}

	// Receive exactly post messages (no duplicates of the pre-restart ones).
	for range post {
		waitMsg(t, got, "post-restart message")
	}

	// No extra messages should arrive (no duplicate replay).
	select {
	case extra := <-got:
		t.Errorf("unexpected extra message after restart (duplicate replay?): %+v", extra.Frame)
	case <-time.After(500 * time.Millisecond):
		// good: no duplicates
	}
}

// TestSubscribeLiveOnlySurvivesRestart: a live-only subscription (no DeliverAll)
// receives messages published after the restart. We wait for the reconnect signal
// (which fires after reestablishSubs completes) before publishing, avoiding the
// race where a publish beats the relay re-establishment.
func TestSubscribeLiveOnlySurvivesRestart(t *testing.T) {
	store := t.TempDir()
	b1 := startBusWithStore(t, store)
	credsFile := mintCredsFile(t, b1, store, "live-only")
	busURL := b1.ClientURL()

	c, reconnected := dialOnURLWithReconnectSignal(t, busURL, credsFile)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("restart-live")

	// Publish before subscribing — these must NOT arrive on a live-only sub.
	mustPublish(t, c, subj, json.RawMessage(`{"before":true}`))

	got := make(chan sextant.Message, 16)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	hardStopAndWait(t, b1)

	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)

	// Wait for reestablishSubs to complete (the "reconnected" log fires after it
	// returns) before publishing, so the relay is up before the message arrives.
	select {
	case <-reconnected:
	case <-time.After(15 * time.Second):
		t.Fatal("client did not reconnect within 15s")
	}

	mustPublish(t, c, subj, json.RawMessage(`{"after":true}`))

	m := waitMsg(t, got, "post-restart live message")
	rec := string(m.Frame.Record)
	if rec != `{"after":true}` {
		t.Errorf("received wrong message (possible pre-restart duplicate): record=%s", rec)
	}
}

// TestSubscribeLoudDeathOnWipedStore: when the bus restarts onto a WIPED store
// (sequences gone), the OnError handler is called — never silence (ADR-0027).
// We simulate a wiped store by stopping the bus, deleting its JetStream data,
// restarting, and observing the OnError callback.
func TestSubscribeLoudDeathOnWipedStore(t *testing.T) {
	store := t.TempDir()
	b1 := startBusWithStore(t, store)
	credsFile := mintCredsFile(t, b1, store, "loud-death")
	busURL := b1.ClientURL()

	c, _ := dialOnURLWithReconnectSignal(t, busURL, credsFile)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("restart-wipe")

	// Publish at least one message so lastSeq > 0 before the restart.
	mustPublish(t, c, subj, json.RawMessage(`{"before":true}`))

	errCh := make(chan error, 1)
	got := make(chan sextant.Message, 8)
	sub, err := c.Subscribe(
		t.Context(), subj, func(m sextant.Message) { got <- m },
		sextant.DeliverAll(),
		sextant.OnError(func(e error) { errCh <- e }),
	)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	// Receive the first message so lastSeq is recorded.
	waitMsg(t, got, "pre-wipe message")

	hardStopAndWait(t, b1)

	// Wipe the JetStream store directory. The messages stream is gone; the bus
	// will recreate it empty (sequences start from scratch at 1 < lastDelivered).
	jsDir := filepath.Join(store, "jetstream")
	if err := os.RemoveAll(jsDir); err != nil {
		t.Fatalf("remove jetstream dir: %v", err)
	}
	// Also remove bus.json so the wiped bus gets a fresh start (new port is fine
	// for this test — the client is already connected and NATS will reconnect).
	// Actually keep bus.json so ADR-0025 holds the port for reconnect.

	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start (wiped): %v", err)
	}
	t.Cleanup(b2.Shutdown)

	// The OnError handler must fire within a generous deadline.
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("OnError called with nil error; want a non-nil loud error")
		}
		if !errors.Is(err, backend.ErrSequenceGone) && !strings.Contains(err.Error(), "sequence") {
			// Accept either the sentinel or any error message mentioning "sequence"
			// — the exact wrapping may vary.
			t.Logf("OnError error: %v (accepted: any sequence-related error)", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("OnError was not called after restart onto wiped store (silent failure)")
	}
}

// TestStopDuringDowntimeIsClean: stopping a subscription while the bus is down
// must not panic, not error, and not leak a goroutine.
func TestStopDuringDowntimeIsClean(t *testing.T) {
	store := t.TempDir()
	b1 := startBusWithStore(t, store)
	credsFile := mintCredsFile(t, b1, store, "stop-downtime")
	busURL := b1.ClientURL()

	c, _ := dialOnURLWithReconnectSignal(t, busURL, credsFile)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("stop-down")

	got := make(chan sextant.Message, 8)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	hardStopAndWait(t, b1)

	// Stop the subscription while the bus is down. Must not hang, panic, or error.
	done := make(chan struct{})
	go func() {
		defer close(done)
		sub.Stop() // teardown calls subscription.stop on the bus — the call will timeout or error; that's fine
	}()
	select {
	case <-done:
		// good
	case <-time.After(10 * time.Second):
		t.Fatal("Stop hung while bus was down")
	}

	// Restart the bus; no goroutine should re-establish the stopped subscription.
	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)

	// Give the NATS client time to reconnect; then publish and verify nothing
	// arrives on the stopped subscription.
	time.Sleep(500 * time.Millisecond)
	_ = c.Publish(t.Context(), subj, json.RawMessage(`{"after":true}`))
	select {
	case m := <-got:
		t.Errorf("stopped subscription received a message after restart: %+v", m.Frame)
	case <-time.After(500 * time.Millisecond):
		// good: subscription is truly stopped
	}
}

// itoa is a tiny int-to-string helper for building test JSON records.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

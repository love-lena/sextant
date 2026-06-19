package bus_test

// Subscription-across-reconnect integration tests (ADR-0027). They reuse the
// embedded-bus restart pattern from restart_test.go but drive the full SDK
// Subscribe path to assert that:
//   - a DeliverAll subscription resumes from last-delivered+1 after a restart
//     of the same store (no duplicates, no gap);
//   - a live-only subscription receives post-restart publishes;
//   - a plain network blip — reconnect to a SURVIVING bus, no restart — keeps
//     the subscription alive (the bus-side relay still exists; the SDK must
//     replace it, not collide with it);
//   - a flap (restart, reconnect, blip again) keeps the subscription alive;
//   - a restart onto a wiped store (sequences gone) fires the OnError handler
//     rather than going silent;
//   - Stop during bus downtime leaves no goroutine leak and no error after.
//
// All waits are deadline-bound (fail-loud, not hang — MEMORY.md feedback_fail_loud).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
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
// been re-established. The "reconnected to the bus" log fires only at the end
// of a completed, non-superseded resume pass (the pass runs asynchronously off
// the NATS dispatcher, see startResumePass in pkg/sextant), so waiting on this
// signal is safe before publishing to a subscription.
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

	// Wait for the resume pass to complete before publishing so the relay is up
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
// (which fires after the resume pass completes) before publishing, avoiding the
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

	// Wait for the resume pass to complete (the "reconnected" log fires at its
	// end) before publishing, so the relay is up before the message arrives.
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

	// The OnError handler must fire within a generous deadline. Errors are
	// stringified across the wire (wireapi.Response.Error), so no sentinel
	// survives — assert non-nil and the stable "sequence gone" message fragment
	// the backend embeds.
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("OnError called with nil error; want a non-nil loud error")
		}
		if !strings.Contains(err.Error(), "sequence gone") {
			t.Errorf("OnError error = %v; want it to carry the backend's 'sequence gone' message", err)
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

// proxy is a single-backend TCP proxy whose live connections can be dropped on
// command, forcing the NATS client to reconnect while the bus stays up. Adapted
// from the TASK-39 review repro.
type proxy struct {
	ln      net.Listener
	backend string

	mu    sync.Mutex
	conns []net.Conn
}

func newProxy(t *testing.T, backend string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{ln: ln, backend: backend}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.dropAll() })
	return p
}

func (p *proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		b, err := net.Dial("tcp", p.backend)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, b)
		p.mu.Unlock()
		go func() { _, _ = io.Copy(b, c); _ = b.Close() }()
		go func() { _, _ = io.Copy(c, b); _ = c.Close() }()
	}
}

func (p *proxy) dropAll() {
	p.mu.Lock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
	p.mu.Unlock()
}

func (p *proxy) url() string { return "nats://" + p.ln.Addr().String() }

// hostOf returns the host:port of a nats:// URL.
func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	return u.Host
}

// assertSubAlive publishes one message and asserts it arrives on got, and that
// no OnError fired on errCh first. The OnError check is a short negative window;
// the delivery check is a hard deadline.
func assertSubAlive(t *testing.T, c *sextant.Client, subj string, record json.RawMessage, got <-chan sextant.Message, errCh <-chan error, label string) {
	t.Helper()
	select {
	case e := <-errCh:
		t.Fatalf("%s: OnError fired (subscription should be alive): %v", label, e)
	case <-time.After(500 * time.Millisecond):
	}
	mustPublish(t, c, subj, record)
	select {
	case <-got:
	case e := <-errCh:
		t.Fatalf("%s: OnError fired instead of a delivery: %v", label, e)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: publish was not delivered — subscription is dead", label)
	}
}

// TestBlipWithoutRestartKeepsSubscriptionAlive: the reviewer-proven Major 1
// case. A client-side reconnect to a SURVIVING bus (TCP drop, no bus restart):
// the bus-side relay for (clientID, subID) still exists, so the SDK's resume
// must replace it (stop-then-subscribe), not collide with it. No OnError; the
// subscription keeps delivering.
func TestBlipWithoutRestartKeepsSubscriptionAlive(t *testing.T) {
	store := t.TempDir()
	b := startBusWithStore(t, store)
	credsFile := mintCredsFile(t, b, store, "blip")

	px := newProxy(t, hostOf(t, b.ClientURL()))
	c, reconnected := dialOnURLWithReconnectSignal(t, px.url(), credsFile)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("blip")
	got := make(chan sextant.Message, 8)
	errCh := make(chan error, 4)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m },
		sextant.OnError(func(e error) { errCh <- e }))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	// One pre-blip delivery so lastSeq > 0 (the resume path uses SinceSeq).
	mustPublish(t, c, subj, json.RawMessage(`{"n":1}`))
	waitMsg(t, got, "pre-blip delivery")

	// Drop the TCP connections; the bus never restarts.
	px.dropAll()

	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not reconnect through the proxy within 20s")
	}

	assertSubAlive(t, c, subj, json.RawMessage(`{"n":2}`), got, errCh, "post-blip")
}

// TestFlapRestartThenBlipKeepsSubscriptionAlive: a full flap — bus restart
// (same store), reconnect, then a plain blip with the new bus still alive. The
// subscription must survive both transitions: the restart resume and the
// blip's replace-the-surviving-relay path.
func TestFlapRestartThenBlipKeepsSubscriptionAlive(t *testing.T) {
	store := t.TempDir()
	b1 := startBusWithStore(t, store)
	credsFile := mintCredsFile(t, b1, store, "flap")

	px := newProxy(t, hostOf(t, b1.ClientURL()))
	c, reconnected := dialOnURLWithReconnectSignal(t, px.url(), credsFile)
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("flap")
	got := make(chan sextant.Message, 8)
	errCh := make(chan error, 4)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m },
		sextant.OnError(func(e error) { errCh <- e }))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	mustPublish(t, c, subj, json.RawMessage(`{"n":1}`))
	waitMsg(t, got, "pre-flap delivery")

	// Leg 1: restart the bus on the same store (same port; the proxy backend
	// address stays valid). The proxy's connections die with the old server.
	hardStopAndWait(t, b1)
	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)

	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not reconnect after the restart within 20s")
	}
	assertSubAlive(t, c, subj, json.RawMessage(`{"n":2}`), got, errCh, "post-restart")

	// Leg 2: plain blip — drop the TCP connections; b2 stays up. The b2-side
	// relay re-created in leg 1 still exists and must be replaced, not collided
	// with.
	px.dropAll()

	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not reconnect after the blip within 20s")
	}
	assertSubAlive(t, c, subj, json.RawMessage(`{"n":3}`), got, errCh, "post-blip")
}

// TestBlipExactlyOnceWhilePublisherKeepsPublishing: exactly-once across a blip
// with traffic in flight (ADR-0027 no-gaps-no-duplicates). Every other test
// publishes only after the reconnected signal; here a second, DIRECTLY
// connected client keeps publishing while the proxied subscriber is dropped and
// straight through the reconnect window. That window is where the surviving
// relay resumes pushing the moment the NATS client re-sends its deliver-subject
// SUB — before the resume pass runs — so its pushes interleave with the new
// relay's replay; the deliver-side monotonic cursor must drop the overlap. The
// test asserts the subscriber sees every n exactly once, in order: a duplicate
// or a gap anywhere across the blip fails it.
func TestBlipExactlyOnceWhilePublisherKeepsPublishing(t *testing.T) {
	store := t.TempDir()
	b := startBusWithStore(t, store)
	subCreds := mintCredsFile(t, b, store, "blip-once-sub")
	pubCreds := mintCredsFile(t, b, store, "blip-once-pub")

	// The subscriber connects through the droppable proxy; the publisher
	// connects directly, so it keeps publishing while the subscriber is out.
	px := newProxy(t, hostOf(t, b.ClientURL()))
	c, reconnected := dialOnURLWithReconnectSignal(t, px.url(), subCreds)
	t.Cleanup(func() { _ = c.Close() })
	pub := dialOnURL(t, b.ClientURL(), pubCreds)
	t.Cleanup(func() { _ = pub.Close() })

	subj := sx.TopicSubject("blip-exactly-once")
	got := make(chan sextant.Message, 4096)
	errCh := make(chan error, 4)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m },
		sextant.OnError(func(e error) { errCh <- e }))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	// One pre-blip delivery so lastSeq > 0 (the resume path uses SinceSeq).
	mustPublish(t, pub, subj, json.RawMessage(`{"n":1}`))
	waitMsg(t, got, "pre-blip delivery")

	// Drop the subscriber's TCP; the bus and the publisher stay up. From here a
	// goroutine publishes n=2,3,... continuously until told to stop.
	px.dropAll()
	stopPub := make(chan struct{})
	type pubResult struct {
		last int // the highest n that was published successfully
		err  error
	}
	pubDone := make(chan pubResult, 1)
	go func() {
		n := 1
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopPub:
				pubDone <- pubResult{last: n}
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := pub.Publish(ctx, subj, json.RawMessage(fmt.Sprintf(`{"n":%d}`, n+1)))
				cancel()
				if err != nil {
					pubDone <- pubResult{last: n, err: err}
					return
				}
				n++
			}
		}
	}()

	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not reconnect through the proxy within 20s")
	}
	// Keep publishing a little past the reconnect so the interleave window
	// (deliver-subject SUB re-sent → reestablish's relay stop) sees traffic.
	time.Sleep(300 * time.Millisecond)
	close(stopPub)
	res := <-pubDone
	if res.err != nil {
		t.Fatalf("the direct publisher failed mid-test: %v", res.err)
	}
	if res.last < 2 {
		t.Fatalf("the publisher only reached n=%d; the blip window saw no traffic", res.last)
	}

	// Exactly-once, in order: n=2..last each appear once. The monotonic cursor
	// also forces delivery order, so any duplicate or gap shows up as a wrong n.
	want := 2
	deadline := time.After(20 * time.Second)
	var lastSeq uint64
	for want <= res.last {
		select {
		case m := <-got:
			var rec struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(m.Frame.Record, &rec); err != nil {
				t.Fatalf("undecodable record %s: %v", m.Frame.Record, err)
			}
			if rec.N != want {
				t.Fatalf("delivery out of order across the blip: got n=%d, want n=%d (a duplicate or a gap)", rec.N, want)
			}
			if m.Sequence <= lastSeq {
				t.Fatalf("non-increasing stream sequence %d after %d (duplicate delivery)", m.Sequence, lastSeq)
			}
			lastSeq = m.Sequence
			want++
		case e := <-errCh:
			t.Fatalf("OnError fired while recovering the blip: %v", e)
		case <-deadline:
			t.Fatalf("timed out waiting for n=%d of %d after the blip (gap)", want, res.last)
		}
	}
	// Nothing extra may trail behind the full set (a late duplicate).
	select {
	case m := <-got:
		t.Fatalf("duplicate delivery after the full set: %s", m.Frame.Record)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestDoubleBlipExactlyOnceWithContinuousPublisher: exactly-once across
// BACK-TO-BACK blips with traffic in flight throughout (ADR-0027). The second
// drop lands the instant the first recovery's "reconnected" completion fires —
// so the recovery of blip one is itself ripped out, and timing permitting the
// second reconnect supersedes a resume pass still finishing its tail. Whatever
// the interleaving, the final pass owns the resume: the subscriber sees every
// n exactly once, in order, and exactly the final recovery completes.
func TestDoubleBlipExactlyOnceWithContinuousPublisher(t *testing.T) {
	store := t.TempDir()
	b := startBusWithStore(t, store)
	subCreds := mintCredsFile(t, b, store, "double-blip-sub")
	pubCreds := mintCredsFile(t, b, store, "double-blip-pub")

	px := newProxy(t, hostOf(t, b.ClientURL()))
	c, reconnected := dialOnURLWithReconnectSignal(t, px.url(), subCreds)
	t.Cleanup(func() { _ = c.Close() })
	pub := dialOnURL(t, b.ClientURL(), pubCreds)
	t.Cleanup(func() { _ = pub.Close() })

	subj := sx.TopicSubject("double-blip")
	got := make(chan sextant.Message, 8192)
	errCh := make(chan error, 8)
	sub, err := c.Subscribe(t.Context(), subj, func(m sextant.Message) { got <- m },
		sextant.OnError(func(e error) { errCh <- e }))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	mustPublish(t, pub, subj, json.RawMessage(`{"n":1}`))
	waitMsg(t, got, "pre-blip delivery")

	// Blip one; the publisher runs continuously from here to the end.
	px.dropAll()
	stopPub := make(chan struct{})
	type pubResult struct {
		last int
		err  error
	}
	pubDone := make(chan pubResult, 1)
	go func() {
		n := 1
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopPub:
				pubDone <- pubResult{last: n}
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := pub.Publish(ctx, subj, json.RawMessage(fmt.Sprintf(`{"n":%d}`, n+1)))
				cancel()
				if err != nil {
					pubDone <- pubResult{last: n, err: err}
					return
				}
				n++
			}
		}
	}()

	// Recovery one...
	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not recover from the first blip within 20s")
	}
	// ...ripped out again immediately: blip two, with the first recovery's
	// replay possibly still in flight.
	px.dropAll()
	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not recover from the second blip within 20s")
	}

	// A little post-recovery traffic, then stop and verify.
	time.Sleep(300 * time.Millisecond)
	close(stopPub)
	res := <-pubDone
	if res.err != nil {
		t.Fatalf("the direct publisher failed mid-test: %v", res.err)
	}
	if res.last < 3 {
		t.Fatalf("the publisher only reached n=%d; the blip windows saw no traffic", res.last)
	}

	// Exactly-once, in order, across both blips and both recoveries.
	want := 2
	deadline := time.After(20 * time.Second)
	var lastSeq uint64
	for want <= res.last {
		select {
		case m := <-got:
			var rec struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(m.Frame.Record, &rec); err != nil {
				t.Fatalf("undecodable record %s: %v", m.Frame.Record, err)
			}
			if rec.N != want {
				t.Fatalf("delivery out of order across the double blip: got n=%d, want n=%d (a duplicate or a gap)", rec.N, want)
			}
			if m.Sequence <= lastSeq {
				t.Fatalf("non-increasing stream sequence %d after %d (duplicate delivery)", m.Sequence, lastSeq)
			}
			lastSeq = m.Sequence
			want++
		case e := <-errCh:
			// Blip two can interrupt a resume rotation mid-flight: a non-fatal
			// deferral notice is legitimate (the final pass retried it). Anything
			// else is a real failure.
			if !errors.Is(e, sextant.ErrResumeDeferred) {
				t.Fatalf("fatal OnError while recovering the double blip: %v", e)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for n=%d of %d after the double blip (gap)", want, res.last)
		}
	}
	select {
	case m := <-got:
		t.Fatalf("duplicate delivery after the full set: %s", m.Frame.Record)
	case <-time.After(500 * time.Millisecond):
	}

	// Exactly one "reconnected" completion per recovery: both signals were
	// consumed above, so a lingering one means a recovery logged twice (sibling
	// passes for one token were not deduped) — a stale buffered signal that
	// would satisfy a later waiter prematurely.
	select {
	case <-reconnected:
		t.Fatal("extra \"reconnected\" completion: one recovery logged twice")
	case <-time.After(500 * time.Millisecond):
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

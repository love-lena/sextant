package sextant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"go.uber.org/goleak"
)

func TestPublishSubscribeRoundTrip(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "pub-sub")
	ctx := t.Context()

	subj := sx.TopicSubject("plan")
	if err := c.Publish(ctx, subj, json.RawMessage(`{"hello":"world"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := make(chan Message, 1)
	sub, err := c.Subscribe(ctx, subj, func(m Message) { got <- m }, DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	select {
	case m := <-got:
		if m.Subject != subj {
			t.Errorf("subject = %q, want %q", m.Subject, subj)
		}
		if m.Frame.Author != c.ID() {
			t.Errorf("author = %q, want %q", m.Frame.Author, c.ID())
		}
		if string(m.Frame.Record) != `{"hello":"world"}` {
			t.Errorf("record = %s", m.Frame.Record)
		}
		if m.BusTime.IsZero() {
			t.Error("BusTime not set")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive the published message")
	}
}

func TestReplayDeliverAll(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "replay")
	ctx := t.Context()
	subj := sx.TopicSubject("log")
	for i := 0; i < 3; i++ {
		if err := c.Publish(ctx, subj, json.RawMessage(`{"n":1}`)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	got := make(chan Message, 8)
	sub, err := c.Subscribe(ctx, subj, func(m Message) { got <- m }, DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	deadline := time.After(5 * time.Second)
	count := 0
	for count < 3 {
		select {
		case <-got:
			count++
		case <-deadline:
			t.Fatalf("replay delivered %d/3 messages", count)
		}
	}
}

func TestPublishRejectsNonMessageSubject(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "reject")
	if err := c.Publish(t.Context(), "sx.control.drain", json.RawMessage(`{}`)); err == nil {
		t.Error("expected Publish to reject a non-messages subject")
	}
}

// TestFetchMessages exercises the pull path (message.read): publish a few, fetch
// from the start, and resume at the returned cursor with no gaps or duplicates.
func TestFetchMessages(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "fetcher")
	ctx := t.Context()
	subj := sx.TopicSubject("pull")
	for i := 0; i < 3; i++ {
		if err := c.Publish(ctx, subj, json.RawMessage(`{"n":1}`)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	frames, next, err := c.FetchMessages(ctx, subj, 0, 10)
	if err != nil {
		t.Fatalf("FetchMessages: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("fetch from 0 got %d frames, want 3", len(frames))
	}
	if frames[0].Author != c.ID() {
		t.Errorf("frame author = %q, want %q", frames[0].Author, c.ID())
	}
	frames2, _, err := c.FetchMessages(ctx, subj, next, 10)
	if err != nil {
		t.Fatalf("FetchMessages resume: %v", err)
	}
	if len(frames2) != 0 {
		t.Fatalf("resume at cursor got %d frames, want 0", len(frames2))
	}
}

// The skew- and invalid-frame quarantine paths are exercised by TestSkewQuarantine
// / TestQuarantinesInvalidFrames in package bus_test (pkg/bus/sdk_integration_test.go):
// they need to inject raw frames that bypass the bus's stamping — something a
// client can no longer do under the allow-list — which the operator seam there
// provides without a production test surface. See docs/conventions/test-features.md.

// TestDeliverDropsNonIncreasingSeq pins the deliver-side monotonic cursor
// (ADR-0027 no-duplicates): within one subscription a single ordered relay
// delivers strictly increasing stream sequences, and a resume relay replays
// only from last+1 — so a non-increasing sequence is always overlap from a
// replaced relay (its in-flight pushes interleaving with the new relay's replay
// around a reconnect blip), never a fresh message. deliver must drop it and
// must never move lastSeq backwards: a regressed cursor would make the next
// resume replay messages the handler already saw.
func TestDeliverDropsNonIncreasingSeq(t *testing.T) {
	c := &Client{skewTol: wire.SkewTolerance, logf: func(string, ...any) {}}
	s := &subscription{}
	var gotSeqs []uint64
	h := func(m Message) { gotSeqs = append(gotSeqs, m.Sequence) }

	mk := func(seq uint64) wireapi.MessageDelivery {
		return wireapi.MessageDelivery{
			SubID:   "sub",
			Subject: sx.TopicSubject("dedup"),
			Seq:     seq,
			BusTime: time.Now(),
			Frame:   wire.New("author", json.RawMessage(`{}`)),
		}
	}

	// 1, 2 fresh (the very first delivery starts from lastSeq 0); 2, 1 overlap
	// (a replaced relay's pushes arriving after the new relay's); 3 fresh; 5
	// fresh (a subject-filtered subscription sees increasing but non-contiguous
	// stream sequences); 4 late overlap arriving after 5 — it must be dropped
	// AND must not regress the cursor; 6 fresh.
	for _, seq := range []uint64{1, 2, 2, 1, 3, 5, 4, 6} {
		c.deliver(mk(seq), h, s)
	}

	want := []uint64{1, 2, 3, 5, 6}
	if !slices.Equal(gotSeqs, want) {
		t.Errorf("delivered sequences = %v, want %v (each exactly once, in order)", gotSeqs, want)
	}
	if last := atomic.LoadUint64(&s.lastSeq); last != 6 {
		t.Errorf("lastSeq = %d, want 6 (the cursor must be monotonic)", last)
	}
}

// TestSubscribeRotatesWhenEpochMovesBeforeRegistration pins the close of the
// residual silent-death window: Subscribe captures the relay generation's
// epoch BEFORE registerSub, so a reconnect completing inside that gap runs its
// resume pass without seeing the subscription — nothing rotates it, the
// buffered message.subscribe succeeds on the restored connection, and
// relayHandler drops every frame forever with no OnError and no log (the
// permanently-silent state ADR-0027 forbids). The post-call staleness re-check
// must detect the moved counter and rotate immediately. The test plants a
// stale epoch through the internal subscribe seam — the deterministic
// equivalent of a reconnect completing inside the gap — and asserts the
// subscription delivers and carries the live counter afterwards.
func TestSubscribeRotatesWhenEpochMovesBeforeRegistration(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "stale-epoch")
	subj := sx.TopicSubject("stale-epoch")

	got := make(chan Message, 8)
	// An epoch the connection never had: every frame delivered to this
	// generation would fail relayHandler's reconnect-count check and be dropped.
	stale := c.nc.Stats().Reconnects + 1
	sub, err := c.subscribe(t.Context(), subj, func(m Message) { got <- m }, subConfig{}, stale)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	if err := c.Publish(t.Context(), subj, json.RawMessage(`{"alive":true}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-got:
		// Delivered: the re-check rotated the doomed generation.
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery: the stale-epoch generation was never rotated (permanently silent subscription)")
	}

	s := sub.(*subscription)
	s.mu.Lock()
	gotEpoch := s.epoch
	s.mu.Unlock()
	if live := c.nc.Stats().Reconnects; gotEpoch != live {
		t.Errorf("generation epoch = %d after the re-check, want the live counter %d", gotEpoch, live)
	}
}

// TestRotationRacingStopUnsubscribesNewestPair pins the cleanup sweep for the
// Stop-races-resume interleaving: when teardown runs against the pre-rotation
// generation while a rotation is mid-flight, the post-rotation sweep
// (stopNewestRelay) must also unsubscribe the rotated-in NATS subscription —
// not only re-stop the bus-side relay — or it lives on the connection,
// receiving nothing, for the client's life. The test reproduces the
// interleaving's end state directly: teardown first, then the rotation that
// was already in flight, then the sweep.
func TestRotationRacingStopUnsubscribesNewestPair(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "stop-race")
	subj := sx.TopicSubject("stop-race")

	sub, err := c.Subscribe(t.Context(), subj, func(Message) {})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	s := sub.(*subscription)

	// Stop the subscription and wait (deadline-bound) for teardown to drop the
	// original delivery subscription off the connection.
	withSub := c.nc.NumSubscriptions()
	sub.Stop()
	base := withSub - 1
	deadline := time.Now().Add(10 * time.Second)
	for c.nc.NumSubscriptions() != base {
		if time.Now().After(deadline) {
			t.Fatalf("teardown did not unsubscribe the delivery subject within 10s (subs=%d, want %d)", c.nc.NumSubscriptions(), base)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The rotation that was mid-flight when Stop landed: it swaps in a fresh
	// (sub-id, natsSub) pair that teardown never saw.
	if err := s.reestablish(c); err != nil {
		t.Fatalf("reestablish: %v", err)
	}
	if got := c.nc.NumSubscriptions(); got != base+1 {
		t.Fatalf("rotation should have added one NATS subscription (subs=%d, want %d)", got, base+1)
	}

	// The post-rotation sweep must remove it again — bus relay AND client-side
	// NATS subscription.
	s.stopNewestRelay()
	if got := c.nc.NumSubscriptions(); got != base {
		t.Errorf("stopNewestRelay left the rotated-in NATS subscription on the connection (subs=%d, want %d)", got, base)
	}
}

// TestSubscribeFailureLeavesNoRegistration pins the failure half of the
// register-before-call ordering: Subscribe registers the subscription before
// the message.subscribe call (so a reconnect firing inside the call window can
// re-establish it), and must deregister and tear it down when the call fails —
// a failed Subscribe leaves nothing for a later reconnect pass to resurrect.
func TestSubscribeFailureLeavesNoRegistration(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "register-window")

	// The bus rejects a subject outside the messages space — a call failure
	// that lands after the registration happened.
	if _, err := c.Subscribe(t.Context(), "sx.control.nope", func(Message) {}); err == nil {
		t.Fatal("expected Subscribe on a non-messages subject to fail")
	}

	c.subsMu.Lock()
	n := len(c.subs)
	c.subsMu.Unlock()
	if n != 0 {
		t.Fatalf("a failed Subscribe left %d subscription(s) registered; want 0", n)
	}
}

// startBusOnStore starts a bus against store and writes bus.json so a later
// restart on the same store reclaims the same port (ADR-0025).
func startBusOnStore(t *testing.T, store string) *bus.Bus {
	t.Helper()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: b.ClientURL()}); err != nil {
		t.Fatalf("write bus.json: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

// stopBusAndFreePort shuts b down and blocks until its port is free, so a
// restart on the same store can rebind it. Deadline-bound, never hangs.
func stopBusAndFreePort(t *testing.T, b *bus.Bus) {
	t.Helper()
	u, err := url.Parse(b.ClientURL())
	if err != nil {
		t.Fatalf("parse bus URL: %v", err)
	}
	b.Shutdown()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ln, lerr := net.Listen("tcp", "127.0.0.1:"+u.Port())
		if lerr == nil {
			_ = ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("port was not released within 5s of Shutdown — cannot restart")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestResumeTransportFailureDefersUntilNextReconnect pins the two-tier resume
// failure contract (ADR-0027 reserves loud death for an IMPOSSIBLE resume, not
// a flaky network): a resume pass whose calls fail on transport — the bus never
// answered — must not stop the subscription. It stays registered, OnError gets
// the non-fatal ErrResumeDeferred notice, and the next reconnect pass retries
// the resume with no gaps and no duplicates. (The fatal tier — a bus-answered
// impossible resume — is pinned by TestSubscribeLoudDeathOnWipedStore in
// pkg/bus.)
func TestResumeTransportFailureDefersUntilNextReconnect(t *testing.T) {
	store := t.TempDir()
	b1 := startBusOnStore(t, store)

	// Mint before the outage: the identity is durable in the store, so the same
	// credential reconnects to the restarted bus.
	creds, _, err := b1.MintClient(t.Context(), "defer-resume", "test")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsFile := writeCreds(t, creds)

	reconnected := make(chan struct{}, 4)
	c, err := Connect(t.Context(), Options{
		URL:       b1.ClientURL(),
		CredsPath: credsFile,
		Logf: func(format string, args ...any) {
			// "reconnected to the bus" logs only at the end of a completed,
			// non-superseded resume pass.
			if strings.Contains(fmt.Sprintf(format, args...), "reconnected to the bus") {
				select {
				case reconnected <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	subj := sx.TopicSubject("defer-resume")
	got := make(chan Message, 16)
	errCh := make(chan error, 4)
	sub, err := c.Subscribe(t.Context(), subj, func(m Message) { got <- m },
		OnError(func(e error) { errCh <- e }))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Stop)

	// One delivery so lastSeq > 0 — the retried resume must resume, not replay.
	if err := c.Publish(t.Context(), subj, json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the pre-outage delivery")
	}

	// Take the bus down and run a resume pass while it is unreachable: every
	// call is a transport failure (the request deadline expires; the bus never
	// answers). This is the deterministic equivalent of a second blip landing
	// inside the resume window. The pass loop is driven directly (no log, no
	// goroutine) with the live counter as its token, so no supersede fires.
	stopBusAndFreePort(t, b1)
	c.reestablishSubs(c.nc.Stats().Reconnects, c.snapshotSubs())

	// Non-fatal tier: the deferral notice fired...
	select {
	case e := <-errCh:
		if !errors.Is(e, ErrResumeDeferred) {
			t.Fatalf("OnError = %v; want a non-fatal notice wrapping ErrResumeDeferred", e)
		}
	default:
		t.Fatal("no OnError notice for a transport-failed resume (silent dead window)")
	}
	// ...the subscription is still registered for the next pass, and not stopped.
	s := sub.(*subscription)
	c.subsMu.Lock()
	_, registered := c.subs[s]
	c.subsMu.Unlock()
	if !registered {
		t.Fatal("a transport-failed resume deregistered the subscription; it must stay registered so the next reconnect retries it")
	}
	if s.stopped.Load() {
		t.Fatal("a transport-failed resume stopped the subscription; a deferral must keep it live")
	}

	// Restart on the same store (same port via bus.json): the real reconnect
	// pass retries the resume and must succeed.
	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)
	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not reconnect within 20s")
	}

	if err := c.Publish(t.Context(), subj, json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatalf("Publish after restart: %v", err)
	}
	select {
	case m := <-got:
		if string(m.Frame.Record) != `{"n":2}` {
			t.Fatalf("post-restart delivery = %s; want {\"n\":2} (anything else is a duplicate replay)", m.Frame.Record)
		}
	case e := <-errCh:
		t.Fatalf("OnError fired instead of a delivery after the retried resume: %v", e)
	case <-time.After(10 * time.Second):
		t.Fatal("the retried resume never delivered — the subscription was lost")
	}
	// No duplicate of n=1 trailing behind the resume.
	select {
	case m := <-got:
		t.Fatalf("unexpected extra delivery after the resume (duplicate): %s", m.Frame.Record)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestResumePassDoesNotBlockCaller pins the async hand-off: startResumePass is
// the entire body of the ReconnectHandler, which runs on the NATS client's
// async-callback dispatcher. Every rotation is deadline-bounded (10s), but a
// pass is unbounded in aggregate, so the hand-off must return immediately — or
// N wedged subscriptions would block every later disconnect/reconnect notice
// for N×10s. With three subscriptions and an unreachable bus (every rotation
// hangs for its full deadline), the call must return in well under a second,
// and the dispatcher must stay usable: the next real reconnect supersedes the
// wedged pass (its token goes stale) and brings every relay back while the
// wedged rotation is still inside its deadline.
func TestResumePassDoesNotBlockCaller(t *testing.T) {
	store := t.TempDir()
	b1 := startBusOnStore(t, store)
	creds, _, err := b1.MintClient(t.Context(), "async-pass", "test")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsFile := writeCreds(t, creds)

	reconnected := make(chan struct{}, 4)
	c, err := Connect(t.Context(), Options{
		URL:       b1.ClientURL(),
		CredsPath: credsFile,
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
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	const n = 3
	got := make(chan string, 32) // subjects of delivered messages
	errCh := make(chan error, 16)
	subjects := make([]string, n)
	for i := range n {
		subj := sx.TopicSubject(fmt.Sprintf("async-pass-%d", i))
		subjects[i] = subj
		sub, err := c.Subscribe(t.Context(), subj,
			func(m Message) { got <- m.Subject },
			OnError(func(e error) {
				select {
				case errCh <- e:
				default:
				}
			}))
		if err != nil {
			t.Fatalf("Subscribe(%s): %v", subj, err)
		}
		t.Cleanup(sub.Stop)
	}

	// Wedge the resume path: with the bus gone, every rotation call hangs for
	// its full 10s deadline.
	stopBusAndFreePort(t, b1)

	start := time.Now()
	c.startResumePass()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("startResumePass blocked its caller for %v with wedged rotations; on the dispatcher this delays every later notification", took)
	}

	// The caller (in production: the dispatcher) is free. Restart the bus and
	// let the real ReconnectHandler fire — its pass supersedes the wedged one
	// (the reconnect moves the counter past the manual pass's token) and must
	// complete, with all relays live, well before the wedged pass's rotations
	// (3×10s) could have drained.
	b2, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	t.Cleanup(b2.Shutdown)
	select {
	case <-reconnected:
	case <-time.After(20 * time.Second):
		t.Fatal("no completed resume pass within 20s of the bus returning")
	}

	// Every subscription delivers again. A deferral notice from the superseded
	// pass's single in-flight rotation is legitimate; anything else is fatal.
	for _, subj := range subjects {
		if err := c.Publish(t.Context(), subj, json.RawMessage(`{"alive":true}`)); err != nil {
			t.Fatalf("Publish(%s): %v", subj, err)
		}
	}
	want := make(map[string]bool, n)
	for _, subj := range subjects {
		want[subj] = true
	}
	deadline := time.After(15 * time.Second)
	for len(want) > 0 {
		select {
		case subj := <-got:
			delete(want, subj)
		case e := <-errCh:
			if !errors.Is(e, ErrResumeDeferred) {
				t.Fatalf("fatal OnError while recovering: %v", e)
			}
		case <-deadline:
			t.Fatalf("subscriptions still silent after the supersede; missing deliveries on %v", want)
		}
	}
}

// TestCloseMidResumePassIsClean pins the pass lifecycle: Close while a resume
// pass is wedged against an unreachable bus must return promptly — the closed
// signal stops the pass at its next subscription boundary and the closed
// connection fails its in-flight rotation call — and nothing may outlive the
// client: no pass goroutine, no subscription bridge, no NATS internals
// (goleak-verified against a baseline taken before the client existed).
func TestCloseMidResumePassIsClean(t *testing.T) {
	ignore := goleak.IgnoreCurrent()

	store := t.TempDir()
	b1 := startBusOnStore(t, store)
	creds, _, err := b1.MintClient(t.Context(), "close-mid-pass", "test")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsFile := writeCreds(t, creds)
	c, err := Connect(t.Context(), Options{
		URL:       b1.ClientURL(),
		CredsPath: credsFile,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Subscriptions on a context the test cancels in-body, so their bridge
	// goroutines wind down before the leak check (not at test-cleanup time).
	subCtx, cancelSubs := context.WithCancel(t.Context())
	defer cancelSubs()
	for i := range 3 {
		subj := sx.TopicSubject(fmt.Sprintf("close-mid-%d", i))
		if _, err := c.Subscribe(subCtx, subj, func(Message) {}); err != nil {
			t.Fatalf("Subscribe(%s): %v", subj, err)
		}
	}

	// Wedge a pass against the dead bus, then Close into it.
	stopBusAndFreePort(t, b1)
	c.startResumePass()

	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("Close took %v with a resume pass in flight; want a prompt, bounded drain", took)
	}

	// Wind down the subscription bridges (their teardown calls fail fast on the
	// closed connection), then verify nothing leaked.
	cancelSubs()
	goleak.VerifyNone(t, ignore)
}

// TestSubscribeStopsOnContextCancel verifies the subscription tears down when
// the caller cancels the context it was created with, not only on Stop.
func TestSubscribeStopsOnContextCancel(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "ctx-cancel")
	subj := sx.TopicSubject("cancel")

	subCtx, cancel := context.WithCancel(t.Context())
	got := make(chan Message, 4)
	sub, err := c.Subscribe(subCtx, subj, func(m Message) { got <- m })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	cancel()                           // cancelling ctx should wind the subscription down
	time.Sleep(300 * time.Millisecond) // let the bridge goroutine stop the consumer

	if err := c.Publish(t.Context(), subj, json.RawMessage(`{"after":"cancel"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case m := <-got:
		t.Errorf("received a message after ctx cancel; subscription should have stopped: %+v", m.Frame)
	case <-time.After(700 * time.Millisecond):
		// good: no delivery after cancellation
	}
}

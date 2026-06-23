package busfeed_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/busfeed"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/sx"
	"go.uber.org/goleak"
)

// TestMain runs goleak after the whole package so a leaked goroutine from any
// test fails loudly. The bus/JetStream stack and the Go runtime spin up
// background goroutines that are not ours; ignore those by signature.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(
		m,
		// NATS server + client run their own background loops for the bus harness.
		goleak.IgnoreTopFunction("github.com/nats-io/nats%2ego.(*Conn).doReconnect"),
		goleak.IgnoreAnyFunction("github.com/nats-io/nats-server/v2/server.(*Server).Run"),
		goleak.IgnoreAnyFunction("github.com/nats-io/nats-server/v2/server.(*Server).startGoRoutine"),
	)
}

// dialEnv stands up an embedded bus and a connected client, mirroring the
// startBus/credsPath/dialClient pattern in pkg/sextant/client_test.go but
// against the exported bus/sextant funcs (this is an external package).
func dialEnv(t *testing.T, id string) (*sextant.Client, string) {
	t.Helper()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	creds, _, err := b.MintClient(t.Context(), id, "test")
	if err != nil {
		t.Fatalf("MintClient(%s): %v", id, err)
	}
	credsFile := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(credsFile, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: credsFile,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", id, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, c.ID()
}

// runStep runs one tea.Cmd to completion on a deadline, returning its tea.Msg.
// It never hangs: a slow or wedged step fails the test instead.
func runStep(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("nil tea.Cmd")
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case m := <-out:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("tea.Cmd did not return within 5s")
		return nil
	}
}

// subscribe runs the Subscribe step and asserts it reports the subscription is
// open (SubscribedMsg), failing on an ErrMsg. After it returns the SDK
// subscription is live, so a subsequent live-only publish is observed.
func subscribe(t *testing.T, ctx context.Context, f *busfeed.Feed) {
	t.Helper()
	switch msg := runStep(t, f.Subscribe(ctx)).(type) {
	case busfeed.SubscribedMsg:
		// open and live
	case busfeed.ErrMsg:
		t.Fatalf("subscribe error: %v", msg.Err)
	default:
		t.Fatalf("Subscribe returned %#v; want SubscribedMsg", msg)
	}
}

// pumpUntilEvent runs Next until it yields an EventMsg or the deadline passes,
// skipping any coalesced DroppedMsg. It returns the received event.
func pumpUntilEvent(t *testing.T, f *busfeed.Feed) sextant.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		switch msg := runStep(t, f.Next()).(type) {
		case busfeed.EventMsg:
			return msg.Message
		case busfeed.DroppedMsg:
			// gap marker; keep pumping
		case busfeed.ErrMsg:
			t.Fatalf("feed error: %v", msg.Err)
		case nil:
			t.Fatal("pump ended before an event arrived")
		}
	}
	t.Fatal("no EventMsg within deadline")
	return sextant.Message{}
}

// TestRoundTrip pins AC-1 and AC-2: a Subscribe tea.Cmd re-yields a published
// frame as an EventMsg, and a self-published message arrives via the same
// subscription — proving the round-trip merge with no optimistic echo. The
// received frame's Author is the client's own id, and its Record is exactly what
// was published, so what the UI renders is the bus echo, not a local copy.
func TestRoundTrip(t *testing.T) {
	c, id := dialEnv(t, "feed-roundtrip")
	subject := sx.TopicSubject("plan")

	f := busfeed.New(c, subject)
	defer f.Stop()
	subscribe(t, t.Context(), f)

	record := json.RawMessage(`{"text":"hello bus"}`)
	pubCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.Publish(pubCtx, subject, record); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := pumpUntilEvent(t, f)
	if got.Frame.Author != id {
		t.Errorf("Author = %q; want self id %q (no optimistic echo: this is the bus echo)", got.Frame.Author, id)
	}
	if string(got.Frame.Record) != string(record) {
		t.Errorf("Record = %s; want %s", got.Frame.Record, record)
	}
	if got.Subject != subject {
		t.Errorf("Subject = %q; want %q", got.Subject, subject)
	}
}

// TestDeliverAllBacklog pins that DeliverAll is a passthrough giving
// backlog-then-live: messages published before Subscribe still arrive on
// subscribe.
func TestDeliverAllBacklog(t *testing.T) {
	c, _ := dialEnv(t, "feed-backlog")
	subject := sx.TopicSubject("backlog")

	pubCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const n = 3
	for i := range n {
		rec := json.RawMessage(`{"i":` + itoa(i) + `}`)
		if err := c.Publish(pubCtx, subject, rec); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	f := busfeed.New(c, subject, sextant.DeliverAll())
	defer f.Stop()
	subscribe(t, t.Context(), f)

	// Drain n events from the backlog; each must be one we published.
	var seen int
	deadline := time.Now().Add(5 * time.Second)
	for seen < n && time.Now().Before(deadline) {
		switch msg := runStep(t, f.Next()).(type) {
		case busfeed.EventMsg:
			seen++
		case busfeed.DroppedMsg:
			// gap marker; keep draining
		case busfeed.ErrMsg:
			t.Fatalf("feed error: %v", msg.Err)
		case nil:
			t.Fatal("pump ended before draining backlog")
		}
	}
	if seen != n {
		t.Fatalf("DeliverAll replayed %d events; want %d", seen, n)
	}
}

// TestStopGoleakClean pins AC-3 teardown: after Stop, no goroutine or
// subscription survives. The package-level goleak.VerifyTestMain catches a
// leaked goroutine; here we also confirm the pump ends cleanly (Next returns nil)
// after Stop drains and closes the buffer.
func TestStopGoleakClean(t *testing.T) {
	c, _ := dialEnv(t, "feed-stop")
	subject := sx.TopicSubject("stop")

	f := busfeed.New(c, subject)
	subscribe(t, t.Context(), f) // open a real, live subscription

	f.Stop()
	f.Stop() // idempotent

	// After Stop the buffer is closed; a Next reports the coalesced drops (none)
	// then returns nil, ending the pump.
	if msg := runStep(t, f.Next()); msg != nil {
		t.Fatalf("Next after Stop = %#v; want nil (clean pump end)", msg)
	}
}

// TestStopViaCtxCancel pins that cancelling the subscribe context tears the feed
// down the same as Stop — goleak-clean, no surviving goroutine.
func TestStopViaCtxCancel(t *testing.T) {
	c, _ := dialEnv(t, "feed-ctx")
	subject := sx.TopicSubject("ctx")

	ctx, cancel := context.WithCancel(t.Context())
	f := busfeed.New(c, subject)
	subscribe(t, ctx, f)
	cancel() // tears down the SDK subscription via ctx
	f.Stop() // also closes the buffer so the pump can end and goleak is clean
}

// waitFor polls cond until it holds or the deadline passes, failing the test on
// a timeout. It synchronizes with the SDK's asynchronous delivery goroutine
// without sleep-and-hope: the condition is the fact the test needs, observed.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not reached within deadline: %s", what)
}

// eventIndex extracts the i field of a test record ({"i":N}) from an EventMsg,
// failing the test on any other message type or a malformed record.
func eventIndex(t *testing.T, msg tea.Msg) int {
	t.Helper()
	ev, ok := msg.(busfeed.EventMsg)
	if !ok {
		t.Fatalf("got %#v; want EventMsg", msg)
	}
	var rec struct {
		I int `json:"i"`
	}
	if err := json.Unmarshal(ev.Message.Frame.Record, &rec); err != nil {
		t.Fatalf("unmarshal record %s: %v", ev.Message.Frame.Record, err)
	}
	return rec.I
}

// TestOverflowFiresDroppedInStreamPosition pins the locked overflow policy AND
// the gap marker's placement, order-strictly. Flooding more frames than the
// buffer holds while the pump is not draining drops the excess and coalesces
// the count — no panic, no block, no silent loss — and the single DroppedMsg
// surfaces in stream position: after EVERY buffered pre-gap event (they all
// arrived before anything was dropped) and before the first post-gap event.
// A marker jumping the queue ahead of buffered events is the regression this
// test exists to catch.
func TestOverflowFiresDroppedInStreamPosition(t *testing.T) {
	c, _ := dialEnv(t, "feed-overflow")
	subject := sx.TopicSubject("overflow")

	f := busfeed.New(c, subject)
	defer f.Stop()
	// Subscribe (live), then never issue Next until after the flood: the buffer
	// fills and overflows.
	subscribe(t, t.Context(), f)

	// Flood well past the buffer capacity. The SDK handler must never block, so
	// the floods all return; the excess is dropped and counted.
	pubCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const flood = busfeed.DefaultBuffer + 64
	const overflow = flood - busfeed.DefaultBuffer
	for i := range flood {
		rec := json.RawMessage(`{"i":` + itoa(i) + `}`)
		if err := c.Publish(pubCtx, subject, rec); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	// Delivery is ordered, so the buffer holds exactly events 0..DefaultBuffer-1
	// once the whole flood has been delivered — observed as the drop counter
	// reaching the overflow.
	waitFor(t, "flood delivered (drop counter at overflow)", func() bool {
		return f.DroppedCount() == overflow
	})

	// Drain one slot, then publish a post-gap event; it enqueues into the freed
	// slot carrying the coalesced gap in-band (the counter returns to zero).
	if got := eventIndex(t, runStep(t, f.Next())); got != 0 {
		t.Fatalf("first pump step yielded event %d; want 0 (the marker must not jump the queue)", got)
	}
	if err := c.Publish(pubCtx, subject, json.RawMessage(`{"i":`+itoa(flood)+`}`)); err != nil {
		t.Fatalf("Publish post-gap: %v", err)
	}
	waitFor(t, "post-gap event enqueued (drop counter back to zero)", func() bool {
		return f.DroppedCount() == 0
	})

	// Order-strict drain: the remaining pre-gap events, in publish order, with
	// no marker interleaved...
	for i := 1; i < busfeed.DefaultBuffer; i++ {
		if got := eventIndex(t, runStep(t, f.Next())); got != i {
			t.Fatalf("pre-gap event out of order: got %d, want %d", got, i)
		}
	}
	// ...then the coalesced marker with the exact count...
	msg := runStep(t, f.Next())
	d, ok := msg.(busfeed.DroppedMsg)
	if !ok {
		t.Fatalf("after the pre-gap events got %#v; want DroppedMsg", msg)
	}
	if d.N != overflow {
		t.Fatalf("DroppedMsg.N = %d; want %d (overflow must be fail-loud and exact)", d.N, overflow)
	}
	// ...then the post-gap event.
	if got := eventIndex(t, runStep(t, f.Next())); got != flood {
		t.Fatalf("post-gap event: got %d, want %d", got, flood)
	}
}

// TestResumeDeferredKeepsThePumpAlive pins the non-fatal tier of the SDK's
// resume-failure contract: an OnError wrapping sextant.ErrResumeDeferred (a
// transport-failed resume the next reconnect retries) surfaces as a
// NON-terminal ResumeDeferredMsg — after everything already buffered, coalesced
// across repeated deferrals — and the pump keeps delivering events afterwards.
// Routing the recoverable tier into the terminal ErrMsg permanently killed the
// pane while the still-registered subscription delivered into a feed nobody
// pumped — the regression this test exists to catch.
func TestResumeDeferredKeepsThePumpAlive(t *testing.T) {
	c, _ := dialEnv(t, "feed-deferred")
	subject := sx.TopicSubject("deferred")

	f := busfeed.New(c, subject)
	defer f.Stop()
	subscribe(t, t.Context(), f)

	// One event already buffered when the deferral lands: the notice must not
	// jump ahead of it.
	pubCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.Publish(pubCtx, subject, json.RawMessage(`{"i":0}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "pre-stall event buffered", func() bool { return f.BufferedCount() == 1 })

	// Two deferrals while the notice is unread: they coalesce into one.
	deferred := fmt.Errorf("%w: subscription on %q delivers nothing until then: timeout", sextant.ErrResumeDeferred, subject)
	f.InjectError(deferred)
	f.InjectError(deferred)

	// The buffered event drains first, then the single notice.
	if got := eventIndex(t, runStep(t, f.Next())); got != 0 {
		t.Fatalf("first pump step yielded event %d; want 0 (the notice must not preempt buffered events)", got)
	}
	msg := runStep(t, f.Next())
	notice, ok := msg.(busfeed.ResumeDeferredMsg)
	if !ok {
		t.Fatalf("after the buffered event got %#v; want ResumeDeferredMsg", msg)
	}
	if !errors.Is(notice.Err, sextant.ErrResumeDeferred) {
		t.Errorf("notice.Err = %v; want a wrapped sextant.ErrResumeDeferred", notice.Err)
	}

	// The pump is alive: a later event (the deferred resume succeeded) is
	// delivered — not a duplicate notice, not a terminal error, not a dead pane.
	if err := c.Publish(pubCtx, subject, json.RawMessage(`{"i":1}`)); err != nil {
		t.Fatalf("Publish post-stall: %v", err)
	}
	if got := eventIndex(t, runStep(t, f.Next())); got != 1 {
		t.Fatalf("post-stall pump step yielded event %d; want 1 (the pump must survive a deferral)", got)
	}
}

// TestFatalOnErrorIsTerminal pins the fatal tier: an OnError that does NOT wrap
// ErrResumeDeferred means the SDK has stopped the subscription, and the feed
// surfaces it as the terminal ErrMsg exactly as before the deferred tier
// existed.
func TestFatalOnErrorIsTerminal(t *testing.T) {
	c, _ := dialEnv(t, "feed-fatal")
	subject := sx.TopicSubject("fatal")

	f := busfeed.New(c, subject)
	defer f.Stop()
	subscribe(t, t.Context(), f)

	fatal := errors.New("sextant: subscription lost after reconnect: sequence gone")
	f.InjectError(fatal)

	msg := runStep(t, f.Next())
	em, ok := msg.(busfeed.ErrMsg)
	if !ok {
		t.Fatalf("got %#v; want terminal ErrMsg", msg)
	}
	if !errors.Is(em.Err, fatal) {
		t.Errorf("ErrMsg.Err = %v; want the injected fatal error", em.Err)
	}
}

// itoa is a tiny dependency-free int-to-string for building test records.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

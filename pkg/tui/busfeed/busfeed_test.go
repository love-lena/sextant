package busfeed_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/tui/busfeed"
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

// TestOverflowFiresDropped pins the locked overflow policy: flooding more frames
// than the buffer holds while the pump is not draining drops the excess,
// coalesces the count, and surfaces a single DroppedMsg{N>0} — no panic, no
// block, no silent loss.
func TestOverflowFiresDropped(t *testing.T) {
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
	for i := range flood {
		rec := json.RawMessage(`{"i":` + itoa(i) + `}`)
		if err := c.Publish(pubCtx, subject, rec); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	// Drain the pump until a DroppedMsg surfaces. Events come first (buffered),
	// then the coalesced drop count.
	var dropped int
	deadline := time.Now().Add(10 * time.Second)
	for dropped == 0 && time.Now().Before(deadline) {
		switch msg := runStep(t, f.Next()).(type) {
		case busfeed.DroppedMsg:
			dropped = msg.N
		case busfeed.EventMsg:
			// buffered event; keep draining
		case busfeed.ErrMsg:
			t.Fatalf("feed error: %v", msg.Err)
		case nil:
			t.Fatal("pump ended before a DroppedMsg surfaced")
		}
	}
	if dropped <= 0 {
		t.Fatalf("DroppedMsg.N = %d; want > 0 (overflow must be fail-loud)", dropped)
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

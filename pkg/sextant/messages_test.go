package sextant

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
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

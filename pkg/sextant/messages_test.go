package sextant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/oklog/ulid/v2"
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

// TestSkewQuarantine injects a frame whose ULID time is far in the past
// (bypassing the bus's stamping, via the operator seam) and verifies the receiver
// quarantines it while still delivering a well-formed message — the SDK re-checks
// the clock on consume, so a frame the bus would never stamp (here operator-
// injected; in the field, replayed pre-skew history) cannot slip through.
func TestSkewQuarantine(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "skew-rx")
	ctx := t.Context()
	subj := sx.TopicSubject("skew")

	// A stale frame: ULID timestamp 10 minutes in the past (> 5m tolerance).
	staleID := ulid.MustNew(ulid.Timestamp(time.Now().Add(-10*time.Minute)), ulid.DefaultEntropy()).String()
	stale := wire.Frame{ID: staleID, Author: "rogue", Kind: wire.KindMessage, Epoch: wire.Epoch, Record: json.RawMessage(`{"stale":true}`)}
	staleBytes, _ := wire.Encode(stale)
	if _, err := b.InjectMessage(ctx, subj, staleBytes); err != nil {
		t.Fatalf("inject stale: %v", err)
	}
	// A good frame, published normally.
	if err := c.Publish(ctx, subj, json.RawMessage(`{"good":true}`)); err != nil {
		t.Fatalf("Publish good: %v", err)
	}

	got := make(chan Message, 4)
	sub, err := c.Subscribe(ctx, subj, func(m Message) { got <- m }, DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	select {
	case m := <-got:
		if m.Frame.ID == staleID {
			t.Fatal("stale (skewed) message was delivered; should have been quarantined")
		}
		if string(m.Frame.Record) != `{"good":true}` {
			t.Errorf("unexpected delivered record: %s", m.Frame.Record)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive the good message")
	}
}

// TestQuarantinesInvalidFrames injects (raw, via the operator seam, bypassing the
// bus's stamping) a wrong-epoch frame and a structurally-malformed one, and
// verifies the receiver delivers only the well-formed message. Clients can no
// longer place a non-conforming frame — the allow-list routes every write through
// the bus — but defense-in-depth still re-checks the wire contract on consume:
// retained history can predate an epoch bump, and a backend is not infallible.
func TestQuarantinesInvalidFrames(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "quar-rx")
	ctx := t.Context()
	subj := sx.TopicSubject("quar")

	// Wrong epoch (otherwise well-formed).
	wrongEpoch := wire.New("rogue", json.RawMessage(`{"epoch":"wrong"}`))
	wrongEpoch.Epoch = wire.Epoch + 1
	weBytes, _ := wire.Encode(wrongEpoch)
	if _, err := b.InjectMessage(ctx, subj, weBytes); err != nil {
		t.Fatalf("inject wrong-epoch: %v", err)
	}
	// Structurally malformed: empty author (Validate rejects it).
	bad := wire.New("", json.RawMessage(`{"bad":true}`))
	badBytes, _ := wire.Encode(bad)
	if _, err := b.InjectMessage(ctx, subj, badBytes); err != nil {
		t.Fatalf("inject malformed: %v", err)
	}
	// A good message.
	if err := c.Publish(ctx, subj, json.RawMessage(`{"good":true}`)); err != nil {
		t.Fatalf("Publish good: %v", err)
	}

	got := make(chan Message, 8)
	sub, err := c.Subscribe(ctx, subj, func(m Message) { got <- m }, DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	select {
	case m := <-got:
		if string(m.Frame.Record) != `{"good":true}` {
			t.Errorf("delivered a quarantined message: record=%s epoch=%d author=%q",
				m.Frame.Record, m.Frame.Epoch, m.Frame.Author)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive the good message")
	}
	// Nothing else should arrive — both bad frames were quarantined.
	select {
	case m := <-got:
		t.Errorf("unexpected second delivery (should have been quarantined): %+v", m.Frame)
	case <-time.After(300 * time.Millisecond):
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

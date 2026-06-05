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

// TestSkewQuarantine injects an envelope whose ULID time is far in the past
// (bypassing Publish, via a second client's raw JetStream publish) and verifies
// the receiver quarantines it while still delivering a well-formed message.
func TestSkewQuarantine(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "skew-rx")
	ctx := t.Context()
	subj := sx.TopicSubject("skew")

	injector := inspectJS(t, b)

	// A stale frame: ULID timestamp 10 minutes in the past (> 5m tolerance).
	staleID := ulid.MustNew(ulid.Timestamp(time.Now().Add(-10*time.Minute)), ulid.DefaultEntropy()).String()
	stale := wire.Frame{ID: staleID, Author: "rogue", Kind: wire.KindMessage, Epoch: wire.Epoch, Record: json.RawMessage(`{"stale":true}`)}
	staleBytes, _ := wire.Encode(stale)
	if _, err := injector.Publish(ctx, subj, staleBytes); err != nil {
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

// TestQuarantinesInvalidFrames injects (raw, bypassing Publish) a wrong-epoch
// frame and a structurally-malformed one, and verifies the receiver delivers
// only the well-formed message — a client can raw-publish to msg.>, so the SDK
// must re-check the wire contract on consume.
func TestQuarantinesInvalidFrames(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "quar-rx")
	ctx := t.Context()
	subj := sx.TopicSubject("quar")
	injector := inspectJS(t, b)

	// Wrong epoch (otherwise well-formed).
	wrongEpoch := wire.New("rogue", json.RawMessage(`{"epoch":"wrong"}`))
	wrongEpoch.Epoch = wire.Epoch + 1
	weBytes, _ := wire.Encode(wrongEpoch)
	if _, err := injector.Publish(ctx, subj, weBytes); err != nil {
		t.Fatalf("inject wrong-epoch: %v", err)
	}
	// Structurally malformed: empty author (Validate rejects it).
	bad := wire.New("", json.RawMessage(`{"bad":true}`))
	badBytes, _ := wire.Encode(bad)
	if _, err := injector.Publish(ctx, subj, badBytes); err != nil {
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

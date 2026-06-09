package sextant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
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
	for range 3 {
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
	for range 3 {
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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

// recorder captures channel notifications for assertions.
type recorder struct {
	mu     sync.Mutex
	events []map[string]any
}

func (r *recorder) notify(ctx context.Context, method string, params any) error {
	if method != channelMethod {
		return fmt.Errorf("unexpected method %q", method)
	}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var ev map[string]any
	if err := json.Unmarshal(b, &ev); err != nil {
		return err
	}
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	return nil
}

func (r *recorder) last(t *testing.T) (content string, meta map[string]any) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		t.Fatal("no channel events recorded")
	}
	ev := r.events[len(r.events)-1]
	content, _ = ev["content"].(string)
	meta, _ = ev["meta"].(map[string]any)
	return content, meta
}

// staticNames builds a pre-warmed cache (frameEvent resolves cached-only).
func staticNames(m map[string]string) *nameCache {
	nc := newNameCache(func(ctx context.Context) ([]sextant.ClientInfo, error) {
		var out []sextant.ClientInfo
		for id, name := range m {
			out = append(out, sextant.ClientInfo{ID: id, DisplayName: name})
		}
		return out, nil
	})
	nc.refresh(context.Background())
	return nc
}

func msg(subject, author, record string, seq uint64) sextant.Message {
	return sextant.Message{
		Frame:    wire.Frame{ID: "01FRAME", Author: author, Kind: "message", Record: wire.Lexicon(record)},
		Subject:  subject,
		Sequence: seq,
	}
}

func msgID(id, subject, author, record string, seq uint64) sextant.Message {
	return sextant.Message{
		Frame:    wire.Frame{ID: id, Author: author, Kind: "message", Record: wire.Lexicon(record)},
		Subject:  subject,
		Sequence: seq,
	}
}

var metaKeyRE = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func TestFrameEventRendersChatMessage(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01A": "alice"}))

	h.frameEvent(msg("msg.topic.plan", "01A", `{"$type":"chat.message","text":"hello there"}`, 7))

	content, meta := rec.last(t)
	if content != "hello there" {
		t.Errorf("content = %q, want the chat.message text", content)
	}
	want := map[string]string{"subject": "msg.topic.plan", "sender": "alice", "sender_id": "01A", "seq": "7", "id": "01FRAME"}
	for k, v := range want {
		if meta[k] != v {
			t.Errorf("meta[%s] = %v, want %v", k, meta[k], v)
		}
	}
	for k := range meta {
		if !metaKeyRE.MatchString(k) {
			t.Errorf("meta key %q not alphanumeric+underscore — the harness drops it silently", k)
		}
	}
}

func TestFrameEventRendersOtherLexiconsAsJSON(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))

	record := `{"$type":"document","title":"T","body":"B"}`
	h.frameEvent(msg("msg.topic.docs", "01Z", record, 1))

	content, meta := rec.last(t)
	if content != record {
		t.Errorf("content = %q, want the compact record JSON", content)
	}
	if meta["sender"] != "01Z" {
		t.Errorf("unknown author should fall back to the raw id, got %v", meta["sender"])
	}
}

func TestSystemEventCarriesEventNotFrameAttrs(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))

	h.systemEvent("resume_lost", "msg.topic.plan", "tail lost")

	content, meta := rec.last(t)
	if content != "tail lost" {
		t.Errorf("content = %q", content)
	}
	if meta["event"] != "resume_lost" || meta["subject"] != "msg.topic.plan" {
		t.Errorf("meta = %v", meta)
	}
	for _, frameKey := range []string{"sender", "sender_id", "seq", "id"} {
		if _, ok := meta[frameKey]; ok {
			t.Errorf("system notice carries frame attr %q", frameKey)
		}
	}
}

func TestResumeNoticeDeferredVsLost(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))
	h.subs["msg.topic.plan"] = stubSub{}

	h.resumeNotice("msg.topic.plan", fmt.Errorf("blip: %w", sextant.ErrResumeDeferred))
	_, meta := rec.last(t)
	if meta["event"] != "resume_deferred" {
		t.Errorf("deferred notice event = %v", meta["event"])
	}
	if len(h.active()) != 1 {
		t.Error("deferred resume must keep the subscription registered")
	}

	h.resumeNotice("msg.topic.plan", errors.New("store wiped"))
	content, meta := rec.last(t)
	if meta["event"] != "resume_lost" {
		t.Errorf("fatal notice event = %v", meta["event"])
	}
	if len(h.active()) != 0 {
		t.Error("fatal resume must drop the subscription")
	}
	for _, want := range []string{"message_read", "message_subscribe"} {
		if !regexp.MustCompile(want).MatchString(content) {
			t.Errorf("lost notice %q missing recovery step %q", content, want)
		}
	}
}

// TestSelfEchoSuppressed proves AC#1: a frame whose id was recorded as a
// just-published self-echo is NOT emitted as a channel event.
func TestSelfEchoSuppressed(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01A": "alice"}))

	// Simulate a publish: record the frame id in the echo set.
	const echoID = "01ECHO_SELF_ID"
	h.echo.record(echoID)

	// Deliver the echo back, as the bus would relay it.
	h.frameEvent(msgID(echoID, "msg.topic.plan", "01A", `{"$type":"chat.message","text":"my own message"}`, 1))

	// The recorder must have captured no events (the echo was dropped).
	rec.mu.Lock()
	n := len(rec.events)
	rec.mu.Unlock()
	if n != 0 {
		t.Errorf("self-echo delivered %d channel event(s), want 0 (AC#1)", n)
	}
}

// TestNonEchoFrameDelivered proves the complementary case: a frame whose id is
// NOT in the echo set is still emitted normally (other subscribers unaffected).
func TestNonEchoFrameDelivered(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01B": "bob"}))

	h.echo.record("01SOME_OTHER_PUBLISHED_ID") // only this id is suppressed

	// A different id from a different sender — must come through.
	h.frameEvent(msgID("01DIFFERENT_ID", "msg.topic.plan", "01B", `{"$type":"chat.message","text":"hi"}`, 2))

	rec.mu.Lock()
	n := len(rec.events)
	rec.mu.Unlock()
	if n != 1 {
		t.Errorf("non-echo frame emitted %d event(s), want 1", n)
	}
}

// TestEchoSetBounded proves AC#4: after echoSetSize publishes the oldest id is
// evicted and no longer treated as a self-echo, so the set does not grow
// without limit.
func TestEchoSetBounded(t *testing.T) {
	s := newSelfEchoSet()

	// Fill the ring exactly: ids "id-0" … "id-255".
	ids := make([]string, echoSetSize)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
		s.record(ids[i])
	}
	// All ids are present.
	for _, id := range ids {
		if !s.contains(id) {
			t.Fatalf("id %q should be in the set before eviction", id)
		}
	}

	// Record one more id — the oldest (ids[0]) should now be evicted.
	s.record("id-overflow")
	if s.contains(ids[0]) {
		t.Errorf("oldest id %q should have been evicted after overflow (AC#4)", ids[0])
	}
	if !s.contains("id-overflow") {
		t.Error("newly recorded id-overflow should be in the set")
	}
	// The rest (ids[1]…) are still present.
	for _, id := range ids[1:] {
		if !s.contains(id) {
			t.Errorf("id %q evicted prematurely", id)
		}
	}
}

type stubSub struct{}

func (stubSub) Stop() {}

func TestUnsubscribe(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))
	h.subs["msg.topic.a"] = stubSub{}
	h.subs["msg.topic.b"] = stubSub{}

	active, err := h.unsubscribe("msg.topic.a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0] != "msg.topic.b" {
		t.Errorf("active after unsubscribe = %v", active)
	}
	if _, err := h.unsubscribe("msg.topic.ghost"); err == nil {
		t.Error("unsubscribing an unknown subject must error with the active list")
	}
}

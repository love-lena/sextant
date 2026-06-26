package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/protocol/wire"
	"github.com/love-lena/sextant/sdk/go"
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

// waitForEvents blocks until the recorder has at least n events or the deadline
// passes, so the async drainLoop has time to forward through frameEvent.
func (r *recorder) waitForEvents(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.events)
		r.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder had %d events, want >= %d within deadline", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// TestInboxDrainWakesContentMode proves M1 (review): a frame arriving on the auto-DM
// channel (c.Inbox(), TASK-55) is emitted as a channel event through frameEvent's
// shared emit path — WITHOUT any explicit message_subscribe. This is the wake
// path a principal DM rides; in CONTENT mode the body is pushed.
func TestInboxDrainWakesContentMode(t *testing.T) {
	clearWakeOnly(t)

	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01PRIN": "lena"}))

	inbox := make(chan sextant.Message, 1)
	stop := make(chan struct{})
	defer close(stop)
	go h.drainLoop(inbox, nil, stop)

	inbox <- msgID("01DM_CONTENT", sx.ClientSubject("01SELF"), "01PRIN",
		`{"$type":"chat.message","text":"ship the v0.2 release"}`, 4)

	rec.waitForEvents(t, 1)
	content, meta := rec.last(t)
	if content != "ship the v0.2 release" {
		t.Errorf("DM wake content = %q, want the principal's message text", content)
	}
	if meta["sender"] != "lena" || meta["sender_id"] != "01PRIN" {
		t.Errorf("DM wake meta = %+v, want sender lena / 01PRIN", meta)
	}
}

// TestInboxDrainWakesWakeOnlyMode proves the wake path composes with wake-only mode:
// a DM produces a content-less wake (no body), via the same frameEvent branch.
func TestInboxDrainWakesWakeOnlyMode(t *testing.T) {
	setWakeOnly(t)

	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01PRIN": "lena"}))

	inbox := make(chan sextant.Message, 1)
	stop := make(chan struct{})
	defer close(stop)
	go h.drainLoop(inbox, nil, stop)

	const body = "secret principal instruction"
	inbox <- msgID("01DM_WAKE", sx.ClientSubject("01SELF"), "01PRIN",
		`{"$type":"chat.message","text":"`+body+`"}`, 9)

	rec.waitForEvents(t, 1)
	content, meta := rec.last(t)
	if strings.Contains(content, body) {
		t.Errorf("wake-only DM content %q must not carry the body", content)
	}
	if meta["wake"] != "1" {
		t.Errorf("wake-only DM meta[wake] = %v, want \"1\"", meta["wake"])
	}
	if _, ok := meta["sender"]; ok {
		t.Error("wake-only DM meta must not carry sender (no body)")
	}
}

// TestInboxDrainSuppressesSelfEcho proves a self-published DM is still dropped on the
// drain path: self-echo is checked first in frameEvent, so a worker DMing itself
// (or its own publish relayed back) produces no wake.
func TestInboxDrainSuppressesSelfEcho(t *testing.T) {
	clearWakeOnly(t)

	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01SELF": "me"}))

	const echoID = "01DM_SELF_ECHO"
	h.echo.record(echoID) // this id was just published by this process

	inbox := make(chan sextant.Message, 1)
	stop := make(chan struct{})
	defer close(stop)
	go h.drainLoop(inbox, nil, stop)

	inbox <- msgID(echoID, sx.ClientSubject("01SELF"), "01SELF",
		`{"$type":"chat.message","text":"note to self"}`, 2)

	// Give the drain a moment, then assert nothing was emitted.
	time.Sleep(100 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Errorf("self-echo DM emitted %d wake event(s), want 0", n)
	}
}

// TestInboxDrainStartIsIdempotent proves startInboxDrain starts at most one drain per
// client object: a second call for the same client is a no-op (it does not
// double-relay every DM). It uses a nil-channel client stand-in by tracking the
// registry directly, since startInboxDrain only reads c.Inbox()/c.Drained() lazily in
// the goroutine.
func TestInboxDrainStartIsIdempotent(t *testing.T) {
	h := newChannelHub((&recorder{}).notify, staticNames(nil))

	var c sextant.Client // zero client: DMs()/Drained() return nil channels (block forever)
	h.startInboxDrain(&c)
	h.startInboxDrain(&c) // second call: must not register a second drain

	h.inboxMu.Lock()
	n := len(h.inboxDrains)
	h.inboxMu.Unlock()
	if n != 1 {
		t.Fatalf("inboxDrains has %d entries, want 1 (idempotent per client)", n)
	}

	// stopInboxDrain unblocks the (idle) goroutine and clears the entry.
	h.stopInboxDrain(&c)
	h.inboxMu.Lock()
	n = len(h.inboxDrains)
	h.inboxMu.Unlock()
	if n != 0 {
		t.Fatalf("inboxDrains has %d entries after stop, want 0", n)
	}
	// A double stop is safe.
	h.stopInboxDrain(&c)
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

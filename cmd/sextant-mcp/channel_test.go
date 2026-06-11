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

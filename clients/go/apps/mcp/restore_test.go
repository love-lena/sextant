package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
	"github.com/love-lena/sextant/protocol/wire"
)

// errPush simulates a failed channel notification.
var errPush = errors.New("push failed")

// hasContent reports whether any captured event's content matched.
func (r *recorder) hasContent(match func(string) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if c, _ := ev["content"].(string); match(c) {
			return true
		}
	}
	return false
}

// TestCatchUpDeliversMissedFramesAndAdvances is the heart of restore: frames
// published while a subscription was dead (seq > since) are replayed as channel
// events, and the durable cursor advances to the read's next cursor so a
// subsequent restore resumes from there.
func TestCatchUpDeliversMissedFramesAndAdvances(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01PEER": "peer"}))
	h.state.addSubject("msg.topic.x", "")

	fetch := func(_ context.Context, _ string, since uint64, _ int) ([]wire.Frame, uint64, error) {
		if since == 0 {
			return []wire.Frame{
				{ID: "01F1", Author: "01PEER", Kind: "message", Record: wire.Lexicon(`{"$type":"chat.message","text":"m1"}`)},
				{ID: "01F2", Author: "01PEER", Kind: "message", Record: wire.Lexicon(`{"$type":"chat.message","text":"m2"}`)},
			}, 7, nil
		}
		return nil, since, nil // caught up
	}
	if !h.catchUp(context.Background(), fetch, "msg.topic.x", 0) {
		t.Error("catchUp returned false (gap not closed) on a clean catch-up")
	}

	if !rec.hasContent(func(s string) bool { return s == "m1" }) || !rec.hasContent(func(s string) bool { return s == "m2" }) {
		t.Errorf("catch-up did not deliver both missed frames (events=%d)", rec.count())
	}
	if _, subs := h.state.snapshot(); subs["msg.topic.x"].Seq != 7 {
		t.Errorf("cursor = %d, want 7 (the read's next cursor)", subs["msg.topic.x"].Seq)
	}
}

// TestCatchUpBoundsReplayAtCap: a long offline gap cannot flood the session —
// replay stops at catchUpCap and tells the agent to message_read the rest.
func TestCatchUpBoundsReplayAtCap(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))
	h.state.addSubject("msg.topic.flood", "")

	var frames []wire.Frame
	for i := 0; i < catchUpCap+50; i++ {
		frames = append(frames, wire.Frame{
			ID: fmt.Sprintf("01F%06d", i), Author: "01PEER", Kind: "message",
			Record: wire.Lexicon(`{"$type":"chat.message","text":"x"}`),
		})
	}
	fetch := func(_ context.Context, _ string, since uint64, _ int) ([]wire.Frame, uint64, error) {
		if since == 0 {
			return frames, uint64(len(frames) + 1), nil
		}
		return nil, since, nil
	}
	h.catchUp(context.Background(), fetch, "msg.topic.flood", 0)

	if n := rec.count(); n > catchUpCap+1 { // capped deliveries + one over-cap notice
		t.Errorf("delivered %d events, want <= cap (%d) + a notice", n, catchUpCap)
	}
	if !rec.hasContent(func(s string) bool { return strings.Contains(s, "more than") }) {
		t.Error("over-cap replay should emit a 'more than N missed' notice")
	}
}

// TestCatchUpStopsOnFailedPush: if a catch-up push fails, catchUp stops and does
// NOT advance the durable cursor past the undelivered frame, so the next restore
// re-reads it (re-delivery beats a silent skip).
func TestCatchUpStopsOnFailedPush(t *testing.T) {
	failing := func(context.Context, string, any) error { return errPush }
	h := newChannelHub(failing, staticNames(map[string]string{"01P": "peer"}))
	h.state.addSubject("msg.topic.x", "")
	h.state.advance("msg.topic.x", 5) // primed cursor

	fetch := func(_ context.Context, _ string, since uint64, _ int) ([]wire.Frame, uint64, error) {
		if since == 5 {
			return []wire.Frame{{ID: "01A", Author: "01P", Kind: "message", Record: wire.Lexicon(`{"$type":"chat.message","text":"m"}`)}}, 9, nil
		}
		return nil, since, nil
	}
	if h.catchUp(context.Background(), fetch, "msg.topic.x", 5) {
		t.Error("catchUp returned true on a failed push; want false (gap still open → keep the gate)")
	}

	if _, subs := h.state.snapshot(); subs["msg.topic.x"].Seq != 5 {
		t.Errorf("cursor = %d after a failed catch-up push, want 5 (no advance → re-read next restore)", subs["msg.topic.x"].Seq)
	}
}

// TestIsWildcardSubject: NATS wildcards are detected so restore skips their
// (unlabelable) catch-up.
func TestIsWildcardSubject(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want bool
	}{
		{"msg.topic.plan", false},
		{"msg.client.01ABC", false},
		{"msg.topic.>", true},
		{"msg.*.plan", true},
	} {
		if got := isWildcardSubject(tc.s); got != tc.want {
			t.Errorf("isWildcardSubject(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestFrameEventDedupsRepeatDelivery: the same frame id delivered twice (the
// restore catch-up↔live overlap) is pushed once.
func TestFrameEventDedupsRepeatDelivery(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01B": "bob"}))
	m := msgID("01DUP", "msg.topic.x", "01B", `{"$type":"chat.message","text":"once"}`, 5)
	h.frameEvent(m)
	h.frameEvent(m)
	if n := rec.count(); n != 1 {
		t.Errorf("delivered %d events for one frame id, want 1 (dedup)", n)
	}
}

// TestFrameEventAdvancesTrackedCursor: a delivered frame advances the durable
// cursor for a tracked subject to the NEXT seq to read (delivered seq + 1, since
// message_read's `since` is inclusive) and never adds an untracked one (the
// auto-inbox).
func TestFrameEventAdvancesTrackedCursor(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01B": "bob"}))
	h.state.addSubject("msg.topic.tracked", "")

	h.frameEvent(msgID("01A", "msg.topic.tracked", "01B", `{"$type":"chat.message","text":"hi"}`, 9))
	h.frameEvent(msgID("01C", "msg.topic.untracked", "01B", `{"$type":"chat.message","text":"yo"}`, 12))

	_, subs := h.state.snapshot()
	if subs["msg.topic.tracked"].Seq != 10 {
		t.Errorf("tracked cursor = %d, want 10 (delivered seq 9 + 1, the next seq to read)", subs["msg.topic.tracked"].Seq)
	}
	if _, ok := subs["msg.topic.untracked"]; ok {
		t.Error("a delivered frame on an untracked subject must not add it to the store")
	}
}

// TestFrameEventNoAdvanceOnFailedPush: if the channel push fails, the cursor is
// NOT advanced — the frame re-delivers on the next restore rather than being
// silently marked caught-up.
func TestFrameEventNoAdvanceOnFailedPush(t *testing.T) {
	failing := func(context.Context, string, any) error { return errPush }
	h := newChannelHub(failing, staticNames(map[string]string{"01B": "bob"}))
	h.state.addSubject("msg.topic.x", "")

	h.frameEvent(msgID("01A", "msg.topic.x", "01B", `{"$type":"chat.message","text":"hi"}`, 9))

	if _, subs := h.state.snapshot(); subs["msg.topic.x"].Seq != 0 {
		t.Errorf("cursor = %d, want 0 — a failed push must not advance the cursor", subs["msg.topic.x"].Seq)
	}
}

// TestDiscardClientClearsSubs: when a drained client is discarded, the stale
// subscriptions bound to it are dropped from h.subs, so restoreSubs rebinds them
// on the replacement client instead of skipping them as "already active". The
// durable substate is untouched (the subjects are restored from there).
func TestDiscardClientClearsSubs(t *testing.T) {
	h := newChannelHub((&recorder{}).notify, staticNames(nil))
	h.subs["msg.topic.a"] = stubSub{}
	h.subs["msg.topic.b"] = stubSub{}
	h.state.addSubject("msg.topic.a", "")
	h.state.addSubject("msg.topic.b", "")

	h.discardClient(nil) // nil client: the inbox-drain stop is a no-op; subs still clear

	if len(h.subs) != 0 {
		t.Errorf("discardClient left %d stale subs, want 0 (restore must rebind on the new client)", len(h.subs))
	}
	if _, subs := h.state.snapshot(); len(subs) != 2 {
		t.Errorf("discardClient must NOT touch the durable substate: %v", subs)
	}
}

// TestLiveAdvanceGatedDuringCatchUp is the P1 guard: while a subject's restore
// catch-up is outstanding, a live frame delivers but must NOT advance the cursor
// past the un-backfilled gap (else a then-failed catch-up loses it). Once
// catch-up ends, live advances resume.
func TestLiveAdvanceGatedDuringCatchUp(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01B": "bob"}))
	h.state.addSubject("msg.topic.x", "")
	h.state.advance("msg.topic.x", 5) // primed; gap to backfill starts here

	h.startCatchUp("msg.topic.x")
	h.frameEvent(msgID("01L", "msg.topic.x", "01B", `{"$type":"chat.message","text":"live"}`, 20))
	if _, subs := h.state.snapshot(); subs["msg.topic.x"].Seq != 5 {
		t.Errorf("cursor = %d during catch-up, want 5 (live advance gated so the gap isn't skipped)", subs["msg.topic.x"].Seq)
	}

	h.endCatchUp("msg.topic.x")
	h.frameEvent(msgID("01M", "msg.topic.x", "01B", `{"$type":"chat.message","text":"live2"}`, 21))
	if _, subs := h.state.snapshot(); subs["msg.topic.x"].Seq != 22 {
		t.Errorf("cursor = %d after catch-up ended, want 22 (live advance resumes, seq 21 + 1)", subs["msg.topic.x"].Seq)
	}
}

// TestUnsubscribeBeforeRestoreRemovesFromState: a subject persisted but not yet
// re-bound after a resume (no live h.subs entry) is still removed from the
// durable store on unsubscribe, so a later restore won't re-establish what the
// agent stopped. A truly-unknown subject still errors.
func TestUnsubscribeBeforeRestoreRemovesFromState(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))
	h.state.addSubject("msg.topic.persisted", "") // persisted, not in h.subs

	if _, err := h.unsubscribe("msg.topic.persisted"); err != nil {
		t.Fatalf("unsubscribe of a persisted-but-not-live subject errored: %v", err)
	}
	if _, subs := h.state.snapshot(); len(subs) != 0 {
		t.Errorf("unsubscribe left the persisted subject in the durable store: %v", subs)
	}
	if _, err := h.unsubscribe("msg.topic.never"); err == nil {
		t.Error("unsubscribe of a subject in neither the live map nor the store must still error")
	}
}

// TestRestoreBailsOnGenerationChange: if the client is discarded (generation
// bumped) after a restore began, restore bails before touching the client — so
// it can't re-add stale subscriptions the new client's restore would then skip.
// (A nil client is safe here precisely because the bail returns first.)
func TestRestoreBailsOnGenerationChange(t *testing.T) {
	h := newChannelHub((&recorder{}).notify, staticNames(nil))
	h.state.addSubject("msg.topic.x", "")
	staleGen := h.generation()

	h.discardClient(nil) // bumps the generation

	// Stale generation → restore returns on the first iteration, before it would
	// dereference the (nil) client via subscribe. No panic == bailed correctly.
	h.restore(context.Background(), nil, map[string]subjectCursor{"msg.topic.x": {Seq: 5}}, staleGen)
}

// TestUnsubscribeClearsCatchUpGate: an unsubscribe clears a leftover catch-up
// gate (set by a prior failed restore catch-up), so a re-subscribe of the same
// subject in this process isn't silently wedged (live frames would never advance
// the cursor).
func TestUnsubscribeClearsCatchUpGate(t *testing.T) {
	h := newChannelHub((&recorder{}).notify, staticNames(nil))
	h.subs["msg.topic.x"] = stubSub{}
	h.state.addSubject("msg.topic.x", "")
	h.startCatchUp("msg.topic.x") // simulate a gate stuck after a failed catch-up

	if _, err := h.unsubscribe("msg.topic.x"); err != nil {
		t.Fatal(err)
	}
	if h.isCatchingUp("msg.topic.x") {
		t.Error("unsubscribe left the catch-up gate set; a re-subscribe's cursor would wedge")
	}
}

// TestUnsubscribeRemovesFromState: an explicit unsubscribe drops the subject
// from the durable store so a restore won't re-establish it.
func TestUnsubscribeRemovesFromState(t *testing.T) {
	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(nil))
	h.subs["msg.topic.gone"] = stubSub{}
	h.state.addSubject("msg.topic.gone", "")

	if _, err := h.unsubscribe("msg.topic.gone"); err != nil {
		t.Fatal(err)
	}
	if _, subs := h.state.snapshot(); len(subs) != 0 {
		t.Errorf("unsubscribe left the subject in the durable store: %v", subs)
	}
}

// TestRestorePersistedContextRequiresAgentKind: a persisted context_use choice
// is re-pinned on resume ONLY if it still resolves to an agent context. A
// since-deleted/recreated human (or other non-agent) context must NOT be assumed
// (ADR-0029), mirroring use()'s guard.
func TestRestorePersistedContextRequiresAgentKind(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	st := loadSubstate(t.TempDir(), "sess")

	// A persisted context that is now a non-agent (human) identity → refused.
	if err := clictx.Save(clictx.Context{Name: "ctx-h", URL: "u", ID: "01H", Kind: "human", Creds: "/h.creds"}); err != nil {
		t.Fatal(err)
	}
	st.setContext("ctx-h")
	m := &connManager{cf: cf("", t.TempDir(), "", ""), state: st}
	m.restorePersistedContext()
	if m.switched != "" {
		t.Errorf("re-pinned a non-agent context %q on resume — must refuse (ADR-0029)", m.switched)
	}

	// An agent context IS restored.
	if err := clictx.Save(clictx.Context{Name: "ctx-a", URL: "u", ID: "01A", Kind: "agent", Creds: "/a.creds"}); err != nil {
		t.Fatal(err)
	}
	st.setContext("ctx-a")
	m.restorePersistedContext()
	if m.switched != "ctx-a" {
		t.Errorf("agent context not re-pinned on resume: switched=%q, want ctx-a", m.switched)
	}
}

// TestUsePersistsContextForResume is TASK-124 mode C: context_use records the
// switch in the durable state so a fresh process (resume) re-pins that identity
// instead of reverting to the auto-mint id. The second connManager mirrors
// main's pre-pin of switched from the loaded state.
func TestUsePersistsContextForResume(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "agent-b", URL: "nats://b", ID: "01B", Kind: "agent", Creds: "/b.creds"}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()

	// Process 1: switch to agent-b — persisted to the durable store.
	m1 := &connManager{cf: cf("", t.TempDir(), "", ""), state: loadSubstate(dataDir, "sess")}
	m1.mint = failMint(t)
	if err := m1.use("agent-b"); err != nil {
		t.Fatalf("use: %v", err)
	}

	// Process 2 (resume): a fresh connManager loads the same state; main pre-pins
	// switched from it; resolve connects as agent-b and never mints.
	state2 := loadSubstate(dataDir, "sess")
	ctxName, _ := state2.snapshot()
	if ctxName != "agent-b" {
		t.Fatalf("persisted context = %q, want agent-b", ctxName)
	}
	m2 := &connManager{cf: cf("", t.TempDir(), "", ""), state: state2}
	m2.switched = ctxName
	m2.mint = failMint(t)
	rc, err := m2.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != "agent-b" {
		t.Fatalf("resume resolved %+v, want agent-b (mode C: context_use must survive a resume)", rc)
	}
}

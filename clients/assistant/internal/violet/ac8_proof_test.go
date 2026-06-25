package violet

import (
	"context"
	"testing"
)

// This file is the PRODUCTION-FAITHFUL regression seam for canopus's gate
// finding (the ac8-replay-watermark-blocker artifact). zeroSeqReader mirrors the
// real sdkAdapter.FetchMessages EXACTLY: it returns frames with Sequence==0
// (wire.Frame carries no per-frame stream sequence) plus a real batch cursor.
// The two bugs canopus traced (Bug A: gap dropped when wm>0; Bug B: duplicate on
// restart when wm==0) reproduced against the pre-fix code through this reader.
// These tests assert the FIXED behaviour: the cursor-space response-watermark
// replays the gap exactly once and never re-answers across a restart.

// zeroSeqReader mirrors the PRODUCTION sdkAdapter.FetchMessages: frames with
// Sequence==0 and a real batch cursor (next). It honors limit and `since` so the
// replay's limit=1 paging works: it returns the first un-read frame each call
// and the cursor one past it.
type zeroSeqReader struct {
	// frames are the retained DMs, keyed by their REAL stream sequence (used only
	// internally for paging — never leaked onto the returned frame).
	frames []zeroSeqEntry
}

type zeroSeqEntry struct {
	streamSeq uint64 // the real JetStream stream sequence (internal paging only)
	author    string
	record    []byte
}

func (r *zeroSeqReader) FetchMessages(_ context.Context, _ string, since uint64, limit int) ([]fetchedFrame, uint64, error) {
	var out []fetchedFrame
	next := since
	for _, e := range r.frames {
		// natsbackend.Read uses OptStartSeq=since INCLUSIVE: a frame AT `since` is
		// returned. The watermark (readFrom) is "next to read from", so a frame at
		// streamSeq==since has not yet been read.
		if e.streamSeq < since {
			continue
		}
		out = append(out, fetchedFrame{
			Author:   e.author,
			Sequence: 0, // PRODUCTION-FAITHFUL: the real adapter cannot fill this
			Record:   e.record,
		})
		next = e.streamSeq + 1 // natsbackend.Read cursor contract
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, next, nil
}

// answerAndAdvance simulates answerDM's confirmed-publish-then-advance path
// using the FIXED cursor-space watermark (m.advanceTo), so these proof tests
// exercise exactly the production advance logic.
func answerAndAdvance(t *testing.T, ack *ackStore, msgs []Message) {
	t.Helper()
	for _, m := range msgs {
		// (publish would happen here; assume confirmed)
		if m.advanceTo > 0 {
			if err := ack.advance(m.advanceTo); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}
	}
}

// TestProofBugAFixed (canopus Bug A): wm>0 must NOT drop the offline gap. The
// watermark goes non-zero the moment violet answers its first live DM; a frame
// that arrived during the gap (Sequence==0 from the prod adapter) must still be
// replayed. The fixed replay does NOT call alreadyAnswered(0), so the gap is
// returned.
func TestProofBugAFixed(t *testing.T) {
	ownDM := dmSubject("01VIOLET", "01OPERATOR")
	ack, _ := newAckStore("", ownDM)
	_ = ack.advance(5) // violet answered a live DM up to seq 4; watermark now 5

	reader := &zeroSeqReader{frames: []zeroSeqEntry{
		{streamSeq: 5, author: "01OPERATOR", record: chatMessage("what's blocking v0.5.1?")},
	}}

	msgs, err := replayOfflineGap(context.Background(), reader, ownDM, "01OPERATOR", ack, 100)
	if err != nil {
		t.Fatalf("replayOfflineGap: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Bug A: offline-gap DM dropped with wm>0 — got %d, want 1 (production Sequence==0 path)", len(msgs))
	}
	if msgs[0].advanceTo == 0 {
		t.Fatal("Bug A fix: replayed frame carries no advanceTo cursor — watermark could not advance")
	}
	t.Logf("Bug A fixed: gap replayed with wm>0 (advanceTo=%d)", msgs[0].advanceTo)
}

// TestProofBugBFixed (canopus Bug B): wm==0 must NOT re-answer on restart. A
// replayed frame (Sequence==0) is answered; the watermark advances to its
// advanceTo cursor; a second replay from the SAME ack returns nothing.
func TestProofBugBFixed(t *testing.T) {
	ownDM := dmSubject("01VIOLET", "01OPERATOR")
	ack, _ := newAckStore("", ownDM)

	reader := &zeroSeqReader{frames: []zeroSeqEntry{
		{streamSeq: 5, author: "01OPERATOR", record: chatMessage("is anyone home?")},
	}}

	// Session 1: replay + answer + advance (cursor-space watermark).
	m1, err := replayOfflineGap(context.Background(), reader, ownDM, "01OPERATOR", ack, 100)
	if err != nil {
		t.Fatalf("replay 1: %v", err)
	}
	if len(m1) != 1 {
		t.Fatalf("session 1: want 1 replayed frame, got %d", len(m1))
	}
	answerAndAdvance(t, ack, m1)

	// Session 2 (restart): the watermark advanced, so the same frame is NOT
	// re-delivered.
	m2, err := replayOfflineGap(context.Background(), reader, ownDM, "01OPERATOR", ack, 100)
	if err != nil {
		t.Fatalf("replay 2: %v", err)
	}
	if len(m2) != 0 {
		t.Fatalf("Bug B: watermark did not advance; restart re-delivers — %d duplicate(s)", len(m2))
	}
	t.Logf("Bug B fixed: cursor advanced to %d; no duplicate on restart", ack.readFrom())
}

// TestProofReplayThenLiveTransition (vega's ask, item 3): the watermark is set
// by replay's `next` cursor, then a LIVE frame arrives at d.Seq. Assert the
// message stays exactly-once across that handoff — no drop, no double-act —
// confirming the replay cursor space and the live cursor space are coherent
// (both the JetStream stream sequence).
func TestProofReplayThenLiveTransition(t *testing.T) {
	ownDM := dmSubject("01VIOLET", "01OPERATOR")
	ack, _ := newAckStore("", ownDM)

	// Phase 1 — REPLAY: one gap frame at stream seq 10. Replay returns it with
	// advanceTo = 11 (next cursor). Answer + advance.
	reader := &zeroSeqReader{frames: []zeroSeqEntry{
		{streamSeq: 10, author: "01OPERATOR", record: chatMessage("gap question")},
	}}
	replayed, err := replayOfflineGap(context.Background(), reader, ownDM, "01OPERATOR", ack, 100)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replayed) != 1 || replayed[0].advanceTo != 11 {
		t.Fatalf("replay phase: got %d frames, advanceTo=%v; want 1 frame advanceTo=11",
			len(replayed), advanceTos(replayed))
	}
	answerAndAdvance(t, ack, replayed)
	if got := ack.readFrom(); got != 11 {
		t.Fatalf("after replay: watermark=%d, want 11", got)
	}

	// Phase 2 — LIVE: a live frame arrives at stream seq 11 (the very next
	// sequence — the handoff boundary). onFrame sets advanceTo = Sequence+1 = 12.
	// It must NOT be considered already-answered (11 is not < watermark 11), and
	// after answering, the watermark advances to 12.
	live := Message{
		Author:    "01OPERATOR",
		Subject:   ownDM,
		Record:    chatMessage("live question right after the gap"),
		Sequence:  11,
		advanceTo: 12, // onFrame sets Sequence+1
	}
	if ack.alreadyAnswered(live.Sequence) {
		t.Fatal("live frame at the handoff boundary (seq=11) wrongly treated as already-answered (would DROP it)")
	}
	answerAndAdvance(t, ack, []Message{live})
	if got := ack.readFrom(); got != 12 {
		t.Fatalf("after live: watermark=%d, want 12 (exactly-once across the handoff)", got)
	}

	// A re-delivery of the SAME live frame (e.g. a reconnect replay overlap) must
	// be rejected as already-answered (seq 11 < watermark 12).
	if !ack.alreadyAnswered(11) {
		t.Fatal("a duplicate of the just-answered live frame (seq=11) was NOT rejected (would DOUBLE-act)")
	}
	t.Logf("replay→live transition: exactly-once across the handoff (watermark 5→11→12, no drop, no double-act)")
}

func advanceTos(msgs []Message) []uint64 {
	out := make([]uint64, len(msgs))
	for i, m := range msgs {
		out[i] = m.advanceTo
	}
	return out
}

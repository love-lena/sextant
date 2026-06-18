package violet

import (
	"context"
	"encoding/json"
)

// messageReader is the slice of busClient needed by replayOfflineGap. It is a
// strict subset of busClient (FetchMessages only) so tests can supply a minimal
// fake that does not implement the full interface.
type messageReader interface {
	// FetchMessages pulls a batch of retained frames from `since` (0 = start of
	// retained history). Returns the frames, the next-cursor (the `since` value
	// to pass on the next call for no gaps and no duplicates), and an error.
	FetchMessages(ctx context.Context, subject string, since uint64, limit int) ([]fetchedFrame, uint64, error)
}

// fetchedFrame is one frame as the replay pass sees it — the bus-stamped author
// ULID is the only trust signal (criterion 2: re-attest on each resume; trust is
// re-derived from the bus-stamped field, not from any prior session state).
//
// Sequence is NOT populated by the production sdkAdapter.FetchMessages — the
// SDK's FetchMessages returns []wire.Frame, which carries no per-frame stream
// sequence (only a bus ULID), so the adapter cannot fill it. The replay path
// therefore drives the watermark off the FetchMessages CURSOR (the `next` value),
// not this field. The field is retained only so a test fake can assert behaviour
// against a known sequence; production code must not depend on it.
type fetchedFrame struct {
	Author   string
	Sequence uint64 // 0 from the production adapter; do NOT key replay idempotency on it
	Record   json.RawMessage
}

// replayOfflineGap reads the operator's DM subject from the current ack
// watermark forward, ONE frame at a time, and returns each operator-authored
// frame carrying the cursor-space watermark (advanceTo) to persist after its
// reply lands. It returns frames that:
//
//  1. were authored by the operator (bus-stamped author — the only trust signal,
//     re-attested on each resume: criterion 2), and
//  2. are on violet's OWN DM subject (never a cross-client subject: criterion 1).
//
// The returned messages are ordered oldest-first so the caller can answer them in
// delivery order without gaps.
//
// WATERMARK IN CURSOR SPACE (canopus's gate finding). FetchMessages exposes no
// per-frame sequence, so the replay cannot advance the watermark by a frame's
// sequence — it has none. Instead the replay pages with limit=1: each call
// returns at most one frame plus the `next` cursor (that frame's stream sequence
// + 1). We carry `next` on the Message as advanceTo, and answerDM advances the
// durable watermark to it AFTER the reply is confirmed published — a true
// response-watermark in cursor space, exactly-once across a crash. A crash
// between answering frame N and frame N+1 leaves the cursor at N's `next`, so the
// next startup resumes precisely at N+1 (no drop, no re-answer).
//
// Idempotency on the replay path (criterion 3) is the `since = readFrom()` filter
// alone: we only ever read forward of the persisted watermark. There is NO
// alreadyAnswered(f.Sequence) call here — it was meaningless at the production
// Sequence==0 (it would read as "answered" and silently drop the whole gap), and
// the cursor filter already covers it.
//
// SECURITY (criterion 1): the subject is always dmSubject (violet's own
// two-party DM topic); the caller (Run) never passes a broader subject. The
// operator is re-attested by the bus-stamped author field (criterion 2), not by
// any record-internal claim.
//
// The call is bounded by maxFrames: if more unanswered DMs accumulated while
// violet was offline, the earliest ones are answered first and the rest arrive
// via the live subscription.
//
// NOTE: replayOfflineGap does not call ack.advance() — it is a pure read.
// Advancing the watermark is answerDM's job, done only AFTER a confirmed publish.
func replayOfflineGap(
	ctx context.Context,
	r messageReader,
	dmSubject string,
	operatorID string,
	ack *ackStore,
	maxFrames int,
) ([]Message, error) {
	since := ack.readFrom()

	var out []Message
	for len(out) < maxFrames {
		// limit=1: each fetch yields at most one frame and the `next` cursor that
		// points exactly past it, so every returned Message carries its own
		// per-frame watermark (advanceTo) even though FetchMessages exposes no
		// per-frame sequence.
		frames, next, err := r.FetchMessages(ctx, dmSubject, since, 1)
		if err != nil {
			return out, err
		}
		if len(frames) == 0 {
			break // no more frames on this subject
		}
		f := frames[0]
		// Advance the local paging cursor regardless of whether we answer this
		// frame, so a skipped (non-operator) frame does not wedge the loop.
		if next == 0 || next <= since {
			// Defensive: a cursor that does not advance would loop forever. Stop.
			break
		}
		since = next

		if f.Author == "" {
			continue // malformed: no bus-stamped author; skip (criterion 2)
		}
		// SECURITY criterion 2: re-attest by bus-stamped author ULID on resume.
		// Trust is re-derived from f.Author, not assumed from any prior state.
		// A non-operator frame on the DM subject is not ours to answer — skip it.
		// The local paging cursor already moved past it (since=next), so the loop
		// finds the next operator frame; the PERSISTED watermark only advances when
		// an operator frame is actually answered (to that frame's `next`), so a
		// crash mid-gap re-pages the cheap stranger frames but never re-answers an
		// operator frame.
		if f.Author != operatorID {
			continue
		}
		out = append(out, Message{
			Author:    f.Author,
			Subject:   dmSubject, // always violet's own DM subject (criterion 1)
			Record:    f.Record,
			Sequence:  0,    // replay frames have no live sequence; advance rides advanceTo
			advanceTo: next, // cursor-space watermark to persist after this reply
		})
	}
	return out, nil
}

// replayMaxFrames is the default cap on frames replayed in a single startup
// pass. If more unanswered DMs accumulated while violet was offline, the
// earliest ones are answered first and the rest arrive via the live subscription.
const replayMaxFrames = 100

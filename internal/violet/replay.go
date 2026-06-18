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
type fetchedFrame struct {
	Author   string
	Sequence uint64 // stream sequence (used for idempotency in the live path)
	Record   json.RawMessage
}

// replayOfflineGap reads the operator's DM subject from the current ack
// watermark forward and returns any frames that:
//
//  1. were authored by the operator (bus-stamped author — the only trust signal,
//     re-attested on each resume: criterion 2),
//  2. have NOT already been answered (frameSeq >= ack.readFrom(): criterion 3), and
//  3. are on violet's OWN DM subject (never a cross-client subject: criterion 1).
//
// The returned frames are ordered oldest-first so the caller can answer them in
// delivery order without gaps.
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
// NOTE: do NOT call ack.advance() here. Advancing the watermark is the caller's
// job, done only AFTER the reply is confirmed published (response-watermark
// invariant, criterion 5). replayOfflineGap is a pure read — it does not change
// the ack state.
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
		batch := min(maxFrames-len(out), fetchBatch)
		frames, next, err := r.FetchMessages(ctx, dmSubject, since, batch)
		if err != nil {
			return out, err
		}
		for _, f := range frames {
			if f.Author == "" {
				continue // malformed: no bus-stamped author; skip (criterion 2)
			}
			// SECURITY criterion 2: re-attest by bus-stamped author ULID on resume.
			// Trust is re-derived from f.Author, not assumed from any prior state.
			if f.Author != operatorID {
				continue // not from the operator; not a DM we answer
			}
			// Criterion 3: idempotent — frames already past the watermark are not
			// returned (since >= readFrom ensures this), but if alreadyAnswered fires
			// here it means the cursor raced (e.g. live-path advanced it since we
			// read readFrom). Skip.
			if ack.alreadyAnswered(f.Sequence) {
				continue
			}
			out = append(out, Message{
				Author:   f.Author,
				Subject:  dmSubject, // always violet's own DM subject (criterion 1)
				Record:   f.Record,
				Sequence: f.Sequence,
			})
			if len(out) >= maxFrames {
				break
			}
		}
		if next == 0 || next <= since {
			break // no more frames on this subject
		}
		since = next
	}
	return out, nil
}

// fetchBatch is the per-call page size for replayOfflineGap. Small enough to
// keep individual FetchMessages calls cheap; the loop re-pages if needed.
const fetchBatch = 50

// replayMaxFrames is the default cap on frames replayed in a single startup
// pass. If more unanswered DMs accumulated while violet was offline, the
// earliest ones are answered first and the rest arrive via the live subscription.
const replayMaxFrames = 100

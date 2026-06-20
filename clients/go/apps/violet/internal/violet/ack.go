package violet

import (
	"path/filepath"
	"sync"

	"github.com/love-lena/sextant/clients/go/apps/internal/seqcursor"
)

// ackStore is the durable response-watermark for violet's operator DM subject.
// It tracks the "next sequence to read from" on the DM subject — the cursor to
// pass to FetchMessages so we only read messages NOT yet answered. This is a
// response-watermark, not a read-watermark.
//
// The advance is PER-FRAME on BOTH paths. Replayed frames and live frames both
// flow through answerDM, which calls advance(m.Sequence+1) after each frame's
// reply is confirmed published — so the watermark moves one frame at a time, in
// delivery order, never in a batch. (replayOfflineGap itself does NOT advance
// the cursor; it is a pure read. The advance happens only in answerDM, after the
// publish — see roles.go.)
//
// Because the cursor advances only AFTER a confirmed publish, a crash between
// "received DM" and "published reply" leaves the cursor behind. On the next
// startup/replay, that frame is re-delivered and re-answered. That is the AC8
// guarantee: nothing falls through even across a crash (criterion 5).
//
// SECURITY (criterion 1): only violet's OWN DM subject is tracked. The store is
// keyed by the exact DM subject (msg.topic.dm.<lo>.<hi>) so a replay can only
// ever catch up that one subject — never a cross-client subject. newAckStore
// drops every other subject from a loaded file via the cursor's Retain.
//
// Idempotency (criterion 3): the cursor advance is monotonic (seqcursor.Advance)
// and a replay skips frames already past the watermark. If a frame somehow
// appears both in the replay pass and in the live subscription, the second
// delivery is a no-op.
//
// The durable cursor — the monotonic advance, the atomic write, the
// missing/corrupt-file degrade — is the shared seqcursor.Store (TASK-182).
// ackStore is the violet-side shell: the locked-in subject, the caller's mutex,
// and the per-frame synchronous save that answerDM's response-watermark contract
// requires.
type ackStore struct {
	mu      sync.Mutex
	subject string // the ONE DM subject this store is authorised to track
	cursor  *seqcursor.Store
}

// newAckStore builds an ackStore for violetDMSubject. stateDir is the directory
// where the store file lives (beside other violet substate). If stateDir is ""
// the store is in-memory only (no persistence; tests use this). A corrupt or
// tampered file degrades to clean; any subject other than violetDMSubject in a
// loaded file is dropped (criterion 1 — a cross-client replay must never happen).
func newAckStore(stateDir, violetDMSubject string) (*ackStore, error) {
	path := ""
	if stateDir != "" {
		path = filepath.Join(stateDir, "violet-ack.json")
	}
	cursor, err := seqcursor.Open(path)
	if err != nil {
		// A read error degrades to clean: a re-answer of already-answered DMs is
		// safe (a duplicated reply beats a dropped one). Keep the (empty) store.
		cursor, _ = seqcursor.Open("")
	}
	// SECURITY (criterion 1): keep only the authorised DM subject; drop any other.
	cursor.Retain(violetDMSubject)
	return &ackStore{subject: violetDMSubject, cursor: cursor}, nil
}

// readFrom returns the "next to read from" cursor for the DM subject — the value
// to pass as `since` to FetchMessages. 0 means "from the start of retained
// history" (the FetchMessages convention).
func (a *ackStore) readFrom() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cursor.Since(a.subject)
}

// advance records nextCursor as the new "next to read from" watermark, but only
// forward (monotonic; idempotent; a retry must never rewind). Must be called
// only AFTER the reply is confirmed published (response-watermark, criterion 5).
//
// The single caller is answerDM (roles.go), which passes m.Sequence+1 (one past
// the just-answered frame's sequence) for EVERY answered frame — replayed and
// live alike, since both flow through answerDM. The advance is therefore
// per-frame on both paths, never batched. Do NOT change this to a batch advance:
// per-frame is what makes a crash mid-batch re-deliver only the unanswered tail
// (criterion 3/5) rather than the whole batch or none of it. The save is
// synchronous (per-frame durability) so a crash never loses an answered frame.
func (a *ackStore) advance(nextCursor uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cursor.Advance(a.subject, nextCursor) {
		return nil // idempotent: watermark already here or further ahead (criterion 3)
	}
	return a.cursor.Save()
}

// alreadyAnswered reports whether a frame at frameSeq has already been answered.
// Used in the live path to skip a frame that appeared in both the replay pass
// and the live subscription (criterion 3). frameSeq < readFrom() means the frame
// is before the watermark and was already answered.
func (a *ackStore) alreadyAnswered(frameSeq uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	wm := a.cursor.Since(a.subject)
	return wm > 0 && frameSeq < wm
}

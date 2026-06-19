package violet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
// SECURITY (criterion 1): only violet's OWN DM subject is tracked. The store
// is keyed by the exact DM subject (msg.topic.dm.<lo>.<hi>) so a replay can
// only ever catch up that one subject — never a cross-client subject.
//
// Idempotency (criterion 3): the cursor advance is monotonic and a replay skips
// frames already past the watermark. If a frame somehow appears both in the
// replay pass and in the live subscription, the second delivery is a no-op.
//
// On-disk form: a JSON map subject → nextToRead. Writes are atomic
// (tmp+rename) so a crash mid-write never corrupts the file.
type ackStore struct {
	mu      sync.Mutex
	path    string
	subject string // the ONE DM subject this store is authorised to track
	next    map[string]uint64
}

// newAckStore builds an ackStore for violetDMSubject. stateDir is the directory
// where the store file lives (beside other violet substate). If stateDir is ""
// the store is in-memory only (no persistence; tests use this).
func newAckStore(stateDir, violetDMSubject string) (*ackStore, error) {
	a := &ackStore{
		subject: violetDMSubject,
		next:    map[string]uint64{},
	}
	if stateDir == "" {
		return a, nil // in-memory only
	}
	a.path = filepath.Join(stateDir, "violet-ack.json")
	b, err := os.ReadFile(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, fmt.Errorf("violet: read ack store %s: %w", a.path, err)
	}
	var on struct {
		Next map[string]uint64 `json:"next"`
	}
	if err := json.Unmarshal(b, &on); err != nil {
		// Corrupt store: start clean. A re-answer of already-answered DMs is safe
		// (duplicated reply is better than a dropped one).
		return a, nil
	}
	if on.Next != nil {
		// SECURITY (criterion 1): only restore the entry for the authorised DM
		// subject. Drop any other subject — a cross-client replay must never happen.
		for subj, seq := range on.Next {
			if subj == violetDMSubject {
				a.next[subj] = seq
			}
		}
	}
	return a, nil
}

// readFrom returns the "next to read from" cursor for the DM subject — the
// value to pass as `since` to FetchMessages. 0 means "from the start of
// retained history" (the FetchMessages convention).
func (a *ackStore) readFrom() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next[a.subject]
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
// (criterion 3/5) rather than the whole batch or none of it.
func (a *ackStore) advance(nextCursor uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if nextCursor <= a.next[a.subject] {
		return nil // idempotent: watermark already here or further ahead (criterion 3)
	}
	a.next[a.subject] = nextCursor
	return a.save()
}

// alreadyAnswered reports whether a frame at frameSeq has already been answered.
// Used in the live path to skip a frame that appeared in both the replay pass
// and the live subscription (criterion 3). frameSeq < readFrom() means the
// frame is before the watermark and was already answered.
func (a *ackStore) alreadyAnswered(frameSeq uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	wm := a.next[a.subject]
	return wm > 0 && frameSeq < wm
}

// save writes the store atomically (tmp+rename). Called with mu held.
func (a *ackStore) save() error {
	if a.path == "" {
		return nil // in-memory only; no-op
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return fmt.Errorf("violet: create ack dir: %w", err)
	}
	b, err := json.MarshalIndent(struct {
		Next map[string]uint64 `json:"next"`
	}{Next: a.next}, "", "  ")
	if err != nil {
		return fmt.Errorf("violet: marshal ack store: %w", err)
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("violet: write ack store: %w", err)
	}
	if err := os.Rename(tmp, a.path); err != nil {
		return fmt.Errorf("violet: rename ack store: %w", err)
	}
	return nil
}

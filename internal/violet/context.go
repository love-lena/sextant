package violet

import (
	"sync"
	"time"
)

// warmContext is the in-memory workspace snapshot the three roles share. The
// home-manager (deep refresh) is its sole writer; the conversational role reads
// it to answer instantly — answer-from-context, no per-DM pre-read. This is the
// load-bearing "context-warm" pattern from the handoff: the snapshot is current
// BEFORE any DM arrives, so a question is answered straight from it.
//
// It is guarded by a RWMutex: reads (every answer) never block each other and
// only block during the brief swap at the end of a refresh. The conversational
// role holds the read lock for the microsecond it takes to copy the string out,
// never across the model turn — so a slow refresh never delays an answer.
type warmContext struct {
	mu        sync.RWMutex
	snapshot  string
	updatedAt time.Time
	gen       uint64 // bumped on every swap; lets callers tell stale from fresh
}

// snapshotPlaceholder is what the conversational role sees before the first deep
// refresh lands. Answering from it yields the honest "I'll check" rather than a
// guess — never a confident-but-wrong answer from stale/training knowledge.
const snapshotPlaceholder = "(no workspace snapshot yet — the deep refresh has not completed its first pass; " +
	"if asked something specific, say you'll check rather than answer from memory)"

func newWarmContext() *warmContext {
	return &warmContext{snapshot: snapshotPlaceholder}
}

// set swaps in a fresh snapshot (the home-manager's output-captured turn text).
// An empty snapshot is ignored — a failed refresh must never blank the warm
// context the answers depend on.
func (w *warmContext) set(snapshot string) {
	if snapshot == "" {
		return
	}
	w.mu.Lock()
	w.snapshot = snapshot
	w.updatedAt = time.Now()
	w.gen++
	w.mu.Unlock()
}

// get returns the current snapshot and its generation. The read lock is held
// only for the copy — never across a model turn — so a deep refresh in flight
// cannot stall an answer waiting on this read.
func (w *warmContext) get() (snapshot string, gen uint64) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot, w.gen
}

// age reports how long since the last successful refresh (zero time → never).
func (w *warmContext) age() time.Duration {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.updatedAt.IsZero() {
		return 0
	}
	return time.Since(w.updatedAt)
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/love-lena/sextant/clients/go/apps/internal/seqcursor"
)

// substate is the durable per-session MCP state that lets subscriptions and the
// active context SURVIVE a resume / fresh MCP process (TASK-124). The channelHub
// keeps subscriptions in an in-memory map and the connManager keeps the
// context_use choice in a field; both are lost when the session resumes on a new
// process, so manual subs silently stop delivering and the identity reverts to
// the auto-mint id. We mirror the attest cursor: a small JSON keyed on the stable
// CLAUDE_CODE_SESSION_ID, written under the WRITABLE CLAUDE_PLUGIN_DATA (never the
// read-only plugin root). On connect the server reloads it and self-heals —
// re-pins the context and re-subscribes every subject, catching up from the last
// delivered seq.
type substate struct {
	mu    sync.Mutex
	path  string
	dirty bool // a seq advanced since the last flush (debounces per-frame writes)

	// Context is the context_use'd identity; re-pinned on resume before the
	// auto-mint fallback. Empty until an explicit switch.
	Context string `json:"context,omitempty"`
	// Subjects maps each explicitly-subscribed subject to its restore cursor. The
	// inbox (msg.client.<self>) is auto-subscribed every connect, so it is NOT
	// tracked here; only the manual subscribes that would otherwise be lost.
	Subjects map[string]subjectCursor `json:"subjects"`
}

// subjectCursor is a tracked subscription's restore state: the NEXT stream seq to
// read (0 = unprimed — no frame has been delivered yet) and the deliver mode the
// agent subscribed with ("" = new / live-only, "all" = replay retained history).
// The mode only matters while unprimed: an unprimed "all" restores from the start
// of history (so the downtime window isn't dropped), an unprimed "new" restores
// live-only. Once primed (Seq > 0), a resume catches up from Seq regardless of
// mode (re-replaying the whole backlog would flood).
type subjectCursor struct {
	Seq     uint64 `json:"seq"`
	Deliver string `json:"deliver,omitempty"`
}

// substateFile keys on the session id, sanitized for the filesystem (shared with
// the attest cursor via seqcursor.Path). loadSubstate only reaches it with a
// non-empty session id (it short-circuits to in-memory otherwise), so the
// empty-key in-memory case never fires here.
func substateFile(dataDir, sessionID string) string {
	return seqcursor.Path(dataDir, "mcp-substate", sessionID)
}

// loadSubstate reads the state for sessionID under dataDir. A missing file (first
// run) or any read/parse error yields an empty, usable state — never an error, so
// a corrupt file degrades to "nothing to restore" rather than failing startup.
// An empty dataDir yields an in-memory-only state (path "") that never persists.
func loadSubstate(dataDir, sessionID string) *substate {
	s := &substate{Subjects: map[string]subjectCursor{}}
	// Durability needs BOTH a writable dir AND a stable per-session key. An empty
	// session id would otherwise collapse every server instance onto one shared
	// "no-session" file, leaking one process's context_use choice + subscriptions
	// into another (the identity is already non-reattachable without a session id,
	// ADR-0029). Treat a missing session id like a non-Claude-Code host: in-memory
	// only (path ""), never persisted.
	if dataDir == "" || sessionID == "" {
		return s
	}
	s.path = substateFile(dataDir, sessionID)
	b, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var on substate
	if err := json.Unmarshal(b, &on); err != nil {
		return s
	}
	s.Context = on.Context
	if on.Subjects != nil {
		s.Subjects = on.Subjects
	}
	return s
}

// save writes the state atomically (temp + rename). A nil path (no
// CLAUDE_PLUGIN_DATA) is a no-op. Caller must hold mu.
func (s *substate) save() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
	s.dirty = false
}

// setContext records the context_use'd identity so a resume re-pins it.
func (s *substate) setContext(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Context == name {
		return
	}
	s.Context = name
	s.save()
}

// addSubject records a new explicit subscription with its deliver mode (Seq 0 =
// unprimed). Idempotent: an already-tracked subject keeps its cursor + mode.
// Reports whether it actually inserted (false = already present) so a failed
// subscribe rolls back only what THIS call added, never a pre-existing entry.
func (s *substate) addSubject(subject, deliver string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Subjects[subject]; ok {
		return false
	}
	s.Subjects[subject] = subjectCursor{Deliver: deliver}
	s.save()
	return true
}

// resetCursor returns a subject to unprimed (Seq 0) while keeping it tracked +
// its deliver mode — used after a resume_lost, where the bus said the resume is
// impossible (the store was wiped / history expired), so the old stream position
// is meaningless. Without this, a stale high Seq would wedge the cursor: a wiped
// store restarts at low sequences that never exceed the old value, so advance
// never fires and a restore reads from an impossible cursor.
func (s *substate) resetCursor(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.Subjects[subject]
	if !ok || cur.Seq == 0 {
		return
	}
	cur.Seq = 0
	s.Subjects[subject] = cur
	s.save()
}

// tracked reports whether subject is still in the durable set — restore reads a
// snapshot, so it re-checks this before resubscribing to honor an unsubscribe
// the agent issued after the snapshot was taken (TASK-124).
func (s *substate) tracked(subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.Subjects[subject]
	return ok
}

// removeSubject drops a subscription (explicit unsubscribe) so a restore won't
// re-establish it. Reports whether the subject was present — so unsubscribe can
// honor a stop for a subject that is persisted but not yet re-bound after a
// resume (no live h.subs entry).
func (s *substate) removeSubject(subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Subjects[subject]; !ok {
		return false
	}
	delete(s.Subjects, subject)
	s.save()
	return true
}

// advance moves a subject's catch-up cursor forward as frames are delivered.
// Subsequent advances only mark dirty (the periodic flush persists) so a busy
// subject does not write the file per frame — but the FIRST prime (0 → nonzero)
// writes through immediately: otherwise a kill before the debounce flush would
// leave the on-disk cursor at 0, and a resume would treat the subject as unprimed
// and skip the downtime gap (loss) instead of catching up.
func (s *substate) advance(subject string, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, tracked := s.Subjects[subject]
	if !tracked {
		return // only track subjects we restore (not the auto-inbox or self-DM)
	}
	if seq > cur.Seq {
		firstPrime := cur.Seq == 0
		cur.Seq = seq
		s.Subjects[subject] = cur
		if firstPrime {
			s.save() // durably prime on the first frame; a later kill can't lose it
		} else {
			s.dirty = true
		}
	}
}

// flush persists pending seq advances if any.
func (s *substate) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		s.save()
	}
}

// snapshot returns the context + a copy of the subject→cursor map for restore.
func (s *substate) snapshot() (context string, subjects map[string]subjectCursor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subjects = make(map[string]subjectCursor, len(s.Subjects))
	for k, v := range s.Subjects {
		subjects[k] = v
	}
	return s.Context, subjects
}

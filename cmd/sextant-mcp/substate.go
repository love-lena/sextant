package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// Subjects maps each explicitly-subscribed subject to the last stream seq
	// delivered on it — the catch-up cursor a restore resumes from. The inbox
	// (msg.client.<self>) is auto-subscribed every connect, so it is NOT tracked
	// here; only the manual subscribes that would otherwise be lost.
	Subjects map[string]uint64 `json:"subjects"`
}

// substateFile keys on the session id, sanitized for the filesystem.
func substateFile(dataDir, sessionID string) string {
	name := sessionID
	if name == "" {
		name = "no-session"
	}
	return filepath.Join(dataDir, "mcp-substate", sanitizeSession(name)+".json")
}

// sanitizeSession keeps a session id to a safe flat filename.
func sanitizeSession(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// loadSubstate reads the state for sessionID under dataDir. A missing file (first
// run) or any read/parse error yields an empty, usable state — never an error, so
// a corrupt file degrades to "nothing to restore" rather than failing startup.
// An empty dataDir yields an in-memory-only state (path "") that never persists.
func loadSubstate(dataDir, sessionID string) *substate {
	s := &substate{Subjects: map[string]uint64{}}
	if dataDir == "" {
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

// addSubject records a new explicit subscription (seq 0 = catch up from the
// start of retained history on first restore).
func (s *substate) addSubject(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Subjects[subject]; ok {
		return
	}
	s.Subjects[subject] = 0
	s.save()
}

// removeSubject drops a subscription (explicit unsubscribe) so a restore won't
// re-establish it.
func (s *substate) removeSubject(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Subjects[subject]; !ok {
		return
	}
	delete(s.Subjects, subject)
	s.save()
}

// advance moves a subject's catch-up cursor forward as frames are delivered. It
// only marks dirty (the periodic flush persists) so a busy subject does not
// write the file per frame.
func (s *substate) advance(subject string, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, tracked := s.Subjects[subject]; !tracked {
		return // only track subjects we restore (not the auto-inbox or self-DM)
	}
	if seq > s.Subjects[subject] {
		s.Subjects[subject] = seq
		s.dirty = true
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

// snapshot returns the context + a copy of the subject→seq map for restore.
func (s *substate) snapshot() (context string, subjects map[string]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subjects = make(map[string]uint64, len(s.Subjects))
	for k, v := range s.Subjects {
		subjects[k] = v
	}
	return s.Context, subjects
}

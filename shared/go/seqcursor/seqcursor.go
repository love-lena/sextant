// Package seqcursor is app-internal support: a durable, monotonic, per-subject
// sequence cursor. A subject maps to "the next stream sequence to read" — the
// value passed as FetchMessages' `since` to get no gaps and no duplicates. The
// cursor is the mechanical floor under three resume-durability features that
// each re-declared it before TASK-182: the attest at-most-once hook and violet's
// answered-DM watermark delegate the whole cursor to Store; the richer MCP
// delivery substate (which carries a per-subject deliver mode alongside the
// sequence, so it keeps its own combined on-disk document) shares only the
// filename keying via Sanitize and Path.
//
// The on-disk form is one JSON object, {"next": {subject: seq}}, written
// atomically (temp file + os.Rename, 0600, parent dir created on first save) so
// a crash mid-write can never leave a half-written cursor that re-delivers or
// skips. A missing or corrupt file degrades silently to an empty cursor; an
// empty path makes the cursor in-memory-only (Save is a no-op).
package seqcursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store is a per-subject sequence cursor backed by one JSON file. The zero value
// is unusable; construct it with Open.
//
// Store carries no lock of its own: a caller that shares one Store across
// goroutines wraps it in the caller's own mutex (violet wraps every call in its
// ackStore mutex; attest is single-process-per-turn). Keeping the lock at the
// call site leaves the policy decisions — when to lock, when to flush — with the
// caller, which is the part that differs between sites.
type Store struct {
	path string            // "" => in-memory only; Save is a no-op
	next map[string]uint64 // subject -> next seq to read
}

// onDisk is the JSON envelope. It is a named type (not an anonymous struct at
// each call site) so the wire shape is declared once.
type onDisk struct {
	Next map[string]uint64 `json:"next"`
}

// Open loads the cursor at path. A missing file yields an empty Store with no
// error (a first run has nothing to restore); a corrupt file likewise degrades
// to empty (start clean rather than block the caller) but keeps the path, so the
// next Save rewrites it. A read error other than "not exist" is returned, with
// the path retained so the caller can still Save. An empty path makes the Store
// in-memory-only (Save no-ops); callers compute path with Path and pass "" when
// their keying says "do not persist".
func Open(path string) (*Store, error) {
	s := &Store{path: path, next: map[string]uint64{}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("seqcursor: read %s: %w", path, err)
	}
	var on onDisk
	if err := json.Unmarshal(b, &on); err != nil {
		return s, nil // corrupt: start clean; the next Save rewrites it
	}
	for subject, seq := range on.Next {
		s.next[subject] = seq
	}
	return s, nil
}

// Since returns the next seq to read for subject (0 if untracked — the start of
// retained history, the FetchMessages convention).
func (s *Store) Since(subject string) uint64 { return s.next[subject] }

// Advance moves subject's cursor forward to next, monotonically: a value not
// greater than the current one is a no-op (a stale or out-of-order advance never
// rewinds delivery). It reports whether the in-memory value changed, so a caller
// can decide whether a flush is warranted. Advance is in-memory only; durability
// is Save's job.
func (s *Store) Advance(subject string, next uint64) (changed bool) {
	if next <= s.next[subject] {
		return false
	}
	s.next[subject] = next
	return true
}

// Retain drops every subject not in keep. It is the load-time security filter:
// a tampered or stale file may hold a foreign subject, and dropping it on load
// means a replay can never catch up a subject the caller is not authorised to
// track. It reports whether anything was dropped.
func (s *Store) Retain(keep ...string) (changed bool) {
	allowed := make(map[string]struct{}, len(keep))
	for _, k := range keep {
		allowed[k] = struct{}{}
	}
	for subject := range s.next {
		if _, ok := allowed[subject]; !ok {
			delete(s.next, subject)
			changed = true
		}
	}
	return changed
}

// Save writes the cursor atomically: a temp file beside the destination, then
// os.Rename into place, so a concurrent reader sees either the whole old file or
// the whole new one, never a torn write. The file is 0600 and its parent dir is
// created on first use. Save is a no-op when the path is "" (in-memory only). The
// module never flushes on its own — the caller owns the flush cadence (per-frame,
// debounced, or once-per-invocation).
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("seqcursor: create dir: %w", err)
	}
	b, err := json.MarshalIndent(onDisk{Next: s.next}, "", "  ")
	if err != nil {
		return fmt.Errorf("seqcursor: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("seqcursor: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("seqcursor: rename: %w", err)
	}
	return nil
}

// Sanitize maps a key (a session id) to a filename-safe token: ASCII
// letters/digits and -_ pass through unchanged, every other byte becomes _. It
// keeps a session id usable as a flat filename without escaping its directory.
func Sanitize(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, key)
}

// Path is the canonical session-keyed file path:
// <dataDir>/<subdir>/<sanitized key>.json. It returns "" — meaning in-memory,
// never persisted — when dataDir or key is empty, the "no plugin data dir OR no
// session id => do not persist" rule the MCP sites share.
func Path(dataDir, subdir, key string) string {
	if dataDir == "" || key == "" {
		return ""
	}
	return filepath.Join(dataDir, subdir, Sanitize(key)+".json")
}

package attest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Cursor is the per-session, per-subject delivery cursor that makes the hook
// at-most-once and in-order across session resume (AC#6). It records, per bus
// subject, the next sequence to read — so a second invocation in the same session
// delivers nothing already seen, and the file survives both --resume (keyed on the
// stable CLAUDE_CODE_SESSION_ID) and a plugin update (it lives under the writable,
// persistent CLAUDE_PLUGIN_DATA, never under the read-only plugin root).
//
// The on-disk form is a flat map of subject -> next sequence. "next sequence" is
// the FetchMessages cursor contract: the value to pass as `since` on the following
// read to get no gaps and no duplicates.
//
// Concurrency: concurrent same-session hook runs are NOT locked. Claude Code
// serializes turns within a session, so two attest invocations never race the
// same cursor file in practice; the atomic write-temp-then-rename in Save only
// guards against a crash mid-write, not against concurrent writers.
type Cursor struct {
	path string
	// Next maps a bus subject to the next sequence to read from it.
	Next map[string]uint64 `json:"next"`
}

// cursorFile returns the cursor path for a session under the plugin data dir.
// One file per session id keeps resumes of different sessions independent and
// makes the keying on CLAUDE_CODE_SESSION_ID explicit on disk.
func cursorFile(dataDir, sessionID string) string {
	name := sessionID
	if name == "" {
		name = "no-session"
	}
	return filepath.Join(dataDir, "attest-cursor", sanitize(name)+".json")
}

// sanitize keeps a session id usable as a filename without escaping the dir.
func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// LoadCursor reads the cursor for sessionID under dataDir. A missing file is not
// an error — it yields an empty cursor (first turn of a fresh session). A corrupt
// file is also treated as empty (degrade, never block the turn) but the path is
// retained so the next Save rewrites it clean.
func LoadCursor(dataDir, sessionID string) (*Cursor, error) {
	c := &Cursor{path: cursorFile(dataDir, sessionID), Next: map[string]uint64{}}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("attest: read cursor %s: %w", c.path, err)
	}
	var on struct {
		Next map[string]uint64 `json:"next"`
	}
	if err := json.Unmarshal(b, &on); err != nil {
		// Corrupt cursor: start clean rather than fail the turn.
		return c, nil
	}
	if on.Next != nil {
		c.Next = on.Next
	}
	return c, nil
}

// Since returns the next sequence to read for subject (0 = from the start of
// retained history, the FetchMessages convention).
func (c *Cursor) Since(subject string) uint64 { return c.Next[subject] }

// Advance records next as the cursor for subject, but only forward — a stale or
// out-of-order value never rewinds delivery (at-most-once in the face of a retry).
func (c *Cursor) Advance(subject string, next uint64) {
	if next > c.Next[subject] {
		c.Next[subject] = next
	}
}

// Save persists the cursor atomically (write-temp-then-rename) so a crash mid-write
// can never leave a half-written cursor that re-delivers or skips. It creates the
// parent dir on first use.
func (c *Cursor) Save() error {
	if c.path == "" {
		return errors.New("attest: cursor has no path")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("attest: create cursor dir: %w", err)
	}
	b, err := json.MarshalIndent(struct {
		Next map[string]uint64 `json:"next"`
	}{Next: c.Next}, "", "  ")
	if err != nil {
		return fmt.Errorf("attest: marshal cursor: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("attest: write cursor: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("attest: rename cursor: %w", err)
	}
	return nil
}

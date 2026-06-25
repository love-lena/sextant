package attest

import (
	"github.com/love-lena/sextant/shared/go/seqcursor"
)

// Cursor is the per-session, per-subject delivery cursor that makes the hook
// at-most-once and in-order across session resume (AC#6). It records, per bus
// subject, the next sequence to read — so a second invocation in the same session
// delivers nothing already seen, and the file survives both --resume (keyed on the
// stable CLAUDE_CODE_SESSION_ID) and a plugin update (it lives under the writable,
// persistent CLAUDE_PLUGIN_DATA, never under the read-only plugin root).
//
// The durable cursor itself — the monotonic per-subject advance, the atomic
// write-temp-then-rename, the missing/corrupt-file degrade — is the shared
// seqcursor.Store (TASK-182). Cursor is the attest-side shell: it owns the
// session-keyed filename and exposes exactly the surface attest's callers use.
//
// Concurrency: concurrent same-session hook runs are NOT locked. Claude Code
// serializes turns within a session, so two attest invocations never race the
// same cursor file in practice; the atomic write in Save only guards against a
// crash mid-write, not against concurrent writers.
type Cursor struct {
	store *seqcursor.Store
}

// cursorFile returns the cursor path for a session under the plugin data dir.
// One file per session id keeps resumes of different sessions independent and
// makes the keying on CLAUDE_CODE_SESSION_ID explicit on disk. An empty session
// id falls back to a fixed name rather than a per-process one; an empty dataDir
// yields "" (in-memory only — the production hook short-circuits before here, so
// this is the test/non-Claude-Code host path).
func cursorFile(dataDir, sessionID string) string {
	name := sessionID
	if name == "" {
		name = "no-session"
	}
	return seqcursor.Path(dataDir, "attest-cursor", name)
}

// LoadCursor reads the cursor for sessionID under dataDir. A missing file is not
// an error — it yields an empty cursor (first turn of a fresh session). A corrupt
// file is also treated as empty (degrade, never block the turn); the next Save
// rewrites it clean.
func LoadCursor(dataDir, sessionID string) (*Cursor, error) {
	store, err := seqcursor.Open(cursorFile(dataDir, sessionID))
	if err != nil {
		return &Cursor{store: store}, err
	}
	return &Cursor{store: store}, nil
}

// Since returns the next sequence to read for subject (0 = from the start of
// retained history, the FetchMessages convention).
func (c *Cursor) Since(subject string) uint64 { return c.store.Since(subject) }

// Advance records next as the cursor for subject, but only forward — a stale or
// out-of-order value never rewinds delivery (at-most-once in the face of a retry).
func (c *Cursor) Advance(subject string, next uint64) { c.store.Advance(subject, next) }

// Save persists the cursor atomically (write-temp-then-rename) so a crash mid-write
// can never leave a half-written cursor that re-delivers or skips. It creates the
// parent dir on first use.
func (c *Cursor) Save() error { return c.store.Save() }

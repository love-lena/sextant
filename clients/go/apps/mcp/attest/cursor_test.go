package attest

import (
	"os"
	"path/filepath"
	"testing"
)

const sessionA = "claude-code-session-AAAA"

// TestCursorRoundTrip: a saved cursor reloads to the same per-subject sequences —
// the on-disk durability behind AC#6's "survives session resume".
func TestCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	subj := "msg.client.01KTSELF"

	c, err := LoadCursor(dir, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if c.Since(subj) != 0 {
		t.Fatalf("fresh cursor Since = %d, want 0", c.Since(subj))
	}
	c.Advance(subj, 42)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	// Reload (simulating a later turn / a session resume): the sequence persists.
	again, err := LoadCursor(dir, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if again.Since(subj) != 42 {
		t.Fatalf("reloaded Since = %d, want 42", again.Since(subj))
	}
}

// TestCursorSecondInvocationDeliversNothing: the canonical AC#6 shape. After
// reading up to `next`, a same-session re-invocation starts from `next` — so a
// caller that fetches [since, next) the first time fetches [next, next) the second,
// i.e. nothing already seen.
func TestCursorSecondInvocationDeliversNothing(t *testing.T) {
	dir := t.TempDir()
	subj := "msg.client.01KTSELF"

	// Turn 1: read from 0, the batch's next cursor comes back as 7.
	c1, _ := LoadCursor(dir, sessionA)
	if c1.Since(subj) != 0 {
		t.Fatalf("turn 1 Since = %d, want 0", c1.Since(subj))
	}
	c1.Advance(subj, 7)
	if err := c1.Save(); err != nil {
		t.Fatal(err)
	}

	// Turn 2 (same session): now reads from 7 — past everything turn 1 saw.
	c2, _ := LoadCursor(dir, sessionA)
	if c2.Since(subj) != 7 {
		t.Fatalf("turn 2 Since = %d, want 7 (no re-delivery)", c2.Since(subj))
	}
}

// TestCursorNeverRewinds: a stale Advance can't move the cursor backward (a retry
// or out-of-order batch never re-delivers).
func TestCursorNeverRewinds(t *testing.T) {
	c, err := LoadCursor(t.TempDir(), sessionA)
	if err != nil {
		t.Fatal(err)
	}
	subj := "msg.client.X"
	c.Advance(subj, 10)
	c.Advance(subj, 4) // stale
	if c.Since(subj) != 10 {
		t.Fatalf("Since = %d, want 10 (no rewind)", c.Since(subj))
	}
	c.Advance(subj, 11) // forward
	if c.Since(subj) != 11 {
		t.Fatalf("Since = %d, want 11", c.Since(subj))
	}
}

// TestCursorSessionIsolation: different session ids keep independent cursors, so a
// new session re-reads from the start while the original session's cursor stands.
func TestCursorSessionIsolation(t *testing.T) {
	dir := t.TempDir()
	subj := "msg.client.X"

	a, _ := LoadCursor(dir, sessionA)
	a.Advance(subj, 99)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	b, _ := LoadCursor(dir, "claude-code-session-BBBB")
	if b.Since(subj) != 0 {
		t.Fatalf("a different session's cursor = %d, want 0 (isolated)", b.Since(subj))
	}
}

// TestCursorCorruptFileStartsClean: a corrupt cursor degrades to empty rather than
// erroring the turn, and the next Save rewrites it clean.
func TestCursorCorruptFileStartsClean(t *testing.T) {
	dir := t.TempDir()
	path := cursorFile(dir, sessionA)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCursor(dir, sessionA)
	if err != nil {
		t.Fatalf("corrupt cursor should not error: %v", err)
	}
	if c.Since("any") != 0 {
		t.Fatalf("corrupt cursor should start clean, got %d", c.Since("any"))
	}
	c.Advance("any", 5)
	if err := c.Save(); err != nil {
		t.Fatalf("save after corrupt: %v", err)
	}
	again, _ := LoadCursor(dir, sessionA)
	if again.Since("any") != 5 {
		t.Fatalf("rewrite did not take, got %d", again.Since("any"))
	}
}

package seqcursor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRoundTrip: a saved cursor reloads to the same per-subject sequences — the
// durability the three sites depend on across a process restart.
func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Advance("msg.topic.a", 42)
	s.Advance("msg.topic.b", 7)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Since("msg.topic.a") != 42 {
		t.Errorf("a = %d, want 42", again.Since("msg.topic.a"))
	}
	if again.Since("msg.topic.b") != 7 {
		t.Errorf("b = %d, want 7", again.Since("msg.topic.b"))
	}
}

// TestAdvanceMonotonic: Advance never rewinds, reports whether it changed.
func TestAdvanceMonotonic(t *testing.T) {
	s, _ := Open("")
	if !s.Advance("x", 10) {
		t.Error("first advance should report changed=true")
	}
	if s.Advance("x", 4) {
		t.Error("a lower advance must be a no-op (changed=false)")
	}
	if s.Advance("x", 10) {
		t.Error("an equal advance must be a no-op (changed=false)")
	}
	if s.Since("x") != 10 {
		t.Errorf("Since(x) = %d, want 10 (no rewind)", s.Since("x"))
	}
	if !s.Advance("x", 11) {
		t.Error("a forward advance should report changed=true")
	}
	if s.Since("x") != 11 {
		t.Errorf("Since(x) = %d, want 11", s.Since("x"))
	}
}

// TestSinceUntracked: an untracked subject reads 0 (start of history).
func TestSinceUntracked(t *testing.T) {
	s, _ := Open("")
	if s.Since("never-seen") != 0 {
		t.Errorf("untracked Since = %d, want 0", s.Since("never-seen"))
	}
}

// TestRetainDropsForeign: Retain keeps only the allowed subjects (the load-time
// security filter) and reports whether it dropped anything.
func TestRetainDropsForeign(t *testing.T) {
	s, _ := Open("")
	s.Advance("own", 10)
	s.Advance("foreign-a", 5)
	s.Advance("foreign-b", 99)

	if !s.Retain("own") {
		t.Error("Retain dropped nothing but two foreign subjects were present")
	}
	if s.Since("own") != 10 {
		t.Errorf("own = %d, want 10 (kept)", s.Since("own"))
	}
	if s.Since("foreign-a") != 0 || s.Since("foreign-b") != 0 {
		t.Error("a foreign subject survived Retain")
	}
	if s.Retain("own") {
		t.Error("a second Retain with nothing to drop should report changed=false")
	}
}

// TestMissingFileDegrades: a never-written path loads as empty, not an error.
func TestMissingFileDegrades(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "never.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s.Since("any") != 0 {
		t.Errorf("missing-file cursor not empty: Since = %d", s.Since("any"))
	}
}

// TestCorruptFileDegrades: a garbled file loads as empty (start clean) and the
// next Save rewrites it.
func TestCorruptFileDegrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("corrupt file should not error: %v", err)
	}
	if s.Since("any") != 0 {
		t.Errorf("corrupt cursor not empty: Since = %d", s.Since("any"))
	}
	s.Advance("any", 5)
	if err := s.Save(); err != nil {
		t.Fatalf("save after corrupt: %v", err)
	}
	again, _ := Open(path)
	if again.Since("any") != 5 {
		t.Errorf("rewrite did not take: Since = %d, want 5", again.Since("any"))
	}
}

// TestEmptyPathInMemory: an empty path makes the Store in-memory only — Save is
// a no-op and writes no file.
func TestEmptyPathInMemory(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	s.Advance("x", 9)
	if err := s.Save(); err != nil {
		t.Errorf("Save on an in-memory cursor should be a no-op, got %v", err)
	}
}

// TestSanitize: every byte outside [A-Za-z0-9-_] becomes _.
func TestSanitize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"abc-123_XYZ", "abc-123_XYZ"},
		{"a/b", "a_b"},
		{"a.b:c", "a_b_c"},
		{"..", "__"},
		{"", ""},
	} {
		if got := Sanitize(tc.in); got != tc.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPath: Path is empty (in-memory) when either dataDir or key is empty, and a
// sanitized session-keyed path otherwise.
func TestPath(t *testing.T) {
	if got := Path("", "sub", "key"); got != "" {
		t.Errorf("Path with empty dataDir = %q, want \"\"", got)
	}
	if got := Path("/data", "sub", ""); got != "" {
		t.Errorf("Path with empty key = %q, want \"\"", got)
	}
	want := filepath.Join("/data", "sub", "ab_cd.json")
	if got := Path("/data", "sub", "ab/cd"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// TestAtomicWriteWholeFile: a reader during a Save sees either the whole old
// file or the whole new one — never a torn write. Verified structurally: after a
// Save the destination holds well-formed, complete JSON (the temp+rename
// contract), and no stray temp file is left visible as the cursor.
func TestAtomicWriteWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	s, _ := Open(path)
	s.Advance("x", 1)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// The destination is a complete cursor a fresh reader can load.
	reader, err := Open(path)
	if err != nil {
		t.Fatalf("reader saw an unreadable cursor: %v", err)
	}
	if reader.Since("x") != 1 {
		t.Errorf("reader Since = %d, want 1 (whole file)", reader.Since("x"))
	}
	// The temp file is renamed away, not left beside the cursor.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file lingered after Save: %v", err)
	}
}

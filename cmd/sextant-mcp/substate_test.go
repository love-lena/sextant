package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSubstateRoundTrip is the core of TASK-124's persistence: a fresh process
// (a second loadSubstate of the same session) sees the context + every subject
// and its last delivered seq the prior process recorded.
func TestSubstateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := loadSubstate(dir, "sess-1")
	s.setContext("my-agent")
	s.addSubject("msg.topic.a", "")
	s.addSubject("msg.topic.b", "")
	s.advance("msg.topic.a", 42)
	s.advance("msg.topic.a", 10) // monotonic: a lower seq never moves the cursor back
	s.flush()

	ctx, subs := loadSubstate(dir, "sess-1").snapshot()
	if ctx != "my-agent" {
		t.Errorf("context = %q, want my-agent", ctx)
	}
	if subs["msg.topic.a"].Seq != 42 {
		t.Errorf("a seq = %d, want 42 (and never the lower 10)", subs["msg.topic.a"].Seq)
	}
	if _, ok := subs["msg.topic.b"]; !ok {
		t.Error("subject b not restored")
	}
}

// TestSubstateDeliverModeRoundTrips: the deliver mode persists per subject so a
// restore can distinguish an unprimed deliver="all" (replay history) from a
// deliver="new" (live-only) — they share Seq 0.
func TestSubstateDeliverModeRoundTrips(t *testing.T) {
	dir := t.TempDir()
	s := loadSubstate(dir, "s")
	s.addSubject("hist", "all")
	s.addSubject("live", "")

	_, subs := loadSubstate(dir, "s").snapshot()
	if subs["hist"].Deliver != "all" {
		t.Errorf("hist deliver = %q, want all", subs["hist"].Deliver)
	}
	if subs["live"].Deliver != "" {
		t.Errorf("live deliver = %q, want empty (new)", subs["live"].Deliver)
	}
}

// TestSubstateAddSubjectReportsInsert: addSubject reports whether it inserted, so
// a failed subscribe rolls back only its own insert, never a pre-existing entry.
func TestSubstateAddSubjectReportsInsert(t *testing.T) {
	s := loadSubstate("", "")
	if !s.addSubject("x", "") {
		t.Error("first addSubject should report inserted=true")
	}
	if s.addSubject("x", "all") {
		t.Error("duplicate addSubject should report inserted=false (pre-existing entry)")
	}
}

// TestSubstateFirstPrimeWritesThrough: the first 0→nonzero advance writes to disk
// immediately (so a kill before the debounce flush can't lose the priming), while
// subsequent advances stay debounced until flush.
func TestSubstateFirstPrimeWritesThrough(t *testing.T) {
	dir := t.TempDir()
	s := loadSubstate(dir, "s")
	s.addSubject("x", "")

	s.advance("x", 9) // first prime → write-through
	if _, subs := loadSubstate(dir, "s").snapshot(); subs["x"].Seq != 9 {
		t.Errorf("first prime not durable without flush: on-disk Seq = %d, want 9", subs["x"].Seq)
	}

	s.advance("x", 20) // subsequent → debounced, not yet on disk
	if _, subs := loadSubstate(dir, "s").snapshot(); subs["x"].Seq != 9 {
		t.Errorf("subsequent advance wrote through (want debounced): on-disk Seq = %d, want 9", subs["x"].Seq)
	}

	s.flush()
	if _, subs := loadSubstate(dir, "s").snapshot(); subs["x"].Seq != 20 {
		t.Errorf("flush didn't persist the debounced advance: on-disk Seq = %d, want 20", subs["x"].Seq)
	}
}

// TestSubstateRemoveSubject: an explicit unsubscribe drops the subject so a
// restore won't re-establish it.
func TestSubstateRemoveSubject(t *testing.T) {
	dir := t.TempDir()
	s := loadSubstate(dir, "s")
	s.addSubject("x", "")
	s.removeSubject("x")
	if _, subs := loadSubstate(dir, "s").snapshot(); len(subs) != 0 {
		t.Errorf("removed subject persisted: %v", subs)
	}
}

// TestSubstateResetCursor: after a resume_lost, the cursor resets to 0 (the old
// stream position is gone) but the subject stays tracked with its deliver mode,
// so a later restore re-establishes it from a clean slate instead of wedging on
// a stale high seq.
func TestSubstateResetCursor(t *testing.T) {
	s := loadSubstate("", "")
	s.addSubject("x", "all")
	s.advance("x", 500) // primed at a high seq
	s.resetCursor("x")

	_, subs := s.snapshot()
	sc, ok := subs["x"]
	if !ok {
		t.Fatal("resetCursor dropped the subject; it must stay tracked")
	}
	if sc.Seq != 0 {
		t.Errorf("reset Seq = %d, want 0", sc.Seq)
	}
	if sc.Deliver != "all" {
		t.Errorf("reset Deliver = %q, want all (mode preserved)", sc.Deliver)
	}
}

// TestSubstateAdvanceOnlyTracked: advance is a no-op for an untracked subject
// (the auto-inbox / self-DM are never tracked) and moves a tracked cursor.
func TestSubstateAdvanceOnlyTracked(t *testing.T) {
	s := loadSubstate("", "") // in-memory
	s.advance("untracked", 99)
	if _, subs := s.snapshot(); len(subs) != 0 {
		t.Errorf("advance tracked an unsubscribed subject: %v", subs)
	}
	s.addSubject("y", "")
	s.advance("y", 5)
	if _, subs := s.snapshot(); subs["y"].Seq != 5 {
		t.Errorf("advance didn't move a tracked cursor: %v", subs)
	}
}

// TestSubstateMissingFileDegrades: a never-written session loads as empty rather
// than erroring — a first run has nothing to restore.
func TestSubstateMissingFileDegrades(t *testing.T) {
	s := loadSubstate(t.TempDir(), "never-written")
	if ctx, subs := s.snapshot(); ctx != "" || len(subs) != 0 {
		t.Errorf("missing file should degrade to empty, got ctx=%q subs=%v", ctx, subs)
	}
}

// TestSubstateCorruptFileDegrades: a garbled file degrades to empty rather than
// failing startup.
func TestSubstateCorruptFileDegrades(t *testing.T) {
	dir := t.TempDir()
	p := substateFile(dir, "sess")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ctx, subs := loadSubstate(dir, "sess").snapshot(); ctx != "" || len(subs) != 0 {
		t.Errorf("corrupt file should degrade to empty, got ctx=%q subs=%v", ctx, subs)
	}
}

// TestSubstateInMemoryNeverPersists: an empty dataDir (a non-Claude-Code host)
// yields a usable in-memory-only state that never writes a file.
func TestSubstateInMemoryNeverPersists(t *testing.T) {
	s := loadSubstate("", "")
	s.addSubject("z", "")
	s.flush()
	if s.path != "" {
		t.Errorf("in-memory state has a path: %q", s.path)
	}
}

// TestSubstateNoSessionIsInMemory: a writable dir but NO session id is in-memory
// only — never a shared "no-session" file that would leak state across unrelated
// sessions.
func TestSubstateNoSessionIsInMemory(t *testing.T) {
	s := loadSubstate(t.TempDir(), "")
	if s.path != "" {
		t.Errorf("a missing session id must yield in-memory-only state, got path %q", s.path)
	}
}

package statushook_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/clients/sextant-mcp/internal/statushook"
)

// ShouldFire is the throttle decision (TASK-87): the PostToolUse hook fires on
// every tool call, but the expensive Haiku call runs only when enough time has
// passed since the last one — so "PostToolUse is too frequent" stops being true
// (the firing is cheap; only the throttled Haiku call costs).
func TestShouldFire(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	interval := 45 * time.Second

	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"never run before (zero) fires", time.Time{}, true},
		{"long ago fires", now.Add(-60 * time.Second), true},
		{"exactly at interval fires", now.Add(-45 * time.Second), true},
		{"too recent skips", now.Add(-10 * time.Second), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := statushook.ShouldFire(c.last, now, interval); got != c.want {
				t.Errorf("ShouldFire(last=%v) = %v, want %v", c.last, got, c.want)
			}
		})
	}
}

// State persists the last-fire time per session under the writable plugin-data
// dir, keyed on the session id — the same dir/keying convention as the attest
// cursor, so a session's status throttle resumes across turns.
func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess := "sess-123"

	// A fresh session has no state: LastRun is the zero time (so it fires).
	s, err := statushook.LoadState(dir, sess)
	if err != nil {
		t.Fatalf("LoadState (fresh): %v", err)
	}
	if !s.LastRun.IsZero() {
		t.Fatalf("fresh state LastRun = %v, want zero", s.LastRun)
	}

	when := time.Unix(1_700_000_000, 0)
	s.LastRun = when
	if err := s.Save(dir, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := statushook.LoadState(dir, sess)
	if err != nil {
		t.Fatalf("LoadState (after save): %v", err)
	}
	if !got.LastRun.Equal(when) {
		t.Errorf("reloaded LastRun = %v, want %v", got.LastRun, when)
	}

	// Sanity: the state file lands under the keyed subdir, beside the attest state.
	if _, err := filepath.Rel(dir, statushook.StateFile(dir, sess)); err != nil {
		t.Errorf("StateFile not under dataDir: %v", err)
	}
}

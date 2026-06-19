package statushook_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/mcp/internal/statushook"
)

// RecentActivity digests the tail of a Claude Code transcript (JSONL) into a
// compact string for Haiku: user/assistant prose + tool names, most recent last,
// bounded to maxLines so the Haiku prompt stays small.
func TestRecentActivity(t *testing.T) {
	// A realistic-shape transcript: user prompt, assistant text + a tool_use, a
	// tool_result, then another assistant line. content is an array of blocks.
	lines := []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"fix the dash history bug"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"On it, editing backfill"},{"type":"tool_use","name":"Edit","input":{"file":"app.jsx"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"now rebuilding the UI"},{"type":"tool_use","name":"Bash","input":{"command":"build"}}]}}`,
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := statushook.RecentActivity(path, 10)
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}
	for _, want := range []string{"fix the dash history bug", "On it, editing backfill", "Edit", "now rebuilding the UI", "Bash"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}

	// Bounded: only the last N lines are included.
	got2, err := statushook.RecentActivity(path, 1)
	if err != nil {
		t.Fatalf("RecentActivity(maxLines=1): %v", err)
	}
	if strings.Contains(got2, "fix the dash history bug") {
		t.Errorf("maxLines=1 should only keep the last entry, got:\n%s", got2)
	}
	if !strings.Contains(got2, "now rebuilding the UI") {
		t.Errorf("maxLines=1 should keep the most recent entry, got:\n%s", got2)
	}
}

// A missing or empty transcript is survivable (the hook degrades to silent).
func TestRecentActivityMissing(t *testing.T) {
	if _, err := statushook.RecentActivity(filepath.Join(t.TempDir(), "nope.jsonl"), 10); err == nil {
		t.Error("expected an error for a missing transcript")
	}
}

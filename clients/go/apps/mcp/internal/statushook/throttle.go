// Package statushook is the logic behind the claude-code plugin's PostToolUse
// status hook (TASK-87): a per-agent self-status that a Claude Code hook keeps
// current by calling Haiku to summarize recent activity. This package holds the
// bus-free, side-effect-light pieces — the throttle decision, the per-session
// throttle state, transcript reading, and the minimal Haiku client — so they are
// unit-testable in isolation; the hook wiring (stdin, gating, detach) lives in
// cmd/sextant-mcp/status.go.
//
// Cadence (decided with lena, 2026-06-14): the hook fires on every PostToolUse,
// but the expensive Haiku call is THROTTLED — run at most once per interval. So
// the firing is cheap and only the throttled Haiku call costs; status stays
// current "after the agent decides but before it finishes" without a Haiku call
// per tool.
package statushook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ShouldFire reports whether the Haiku call should run now: only when at least
// interval has elapsed since the last run. A zero last (never run) always fires.
func ShouldFire(last, now time.Time, interval time.Duration) bool {
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= interval
}

// State is the per-session throttle state: when the Haiku call last ran. It lives
// one file per session id under the writable CLAUDE_PLUGIN_DATA, mirroring the
// attest cursor's dir/keying so a session's throttle resumes across turns.
type State struct {
	LastRun time.Time `json:"last_run"`
}

// StateFile is the throttle-state path for a session under the plugin data dir.
func StateFile(dataDir, sessionID string) string {
	name := sessionID
	if name == "" {
		name = "no-session"
	}
	return filepath.Join(dataDir, "status-state", Sanitize(name)+".json")
}

// LoadState reads a session's throttle state. A missing file is not an error —
// it returns a zero State (LastRun zero ⇒ ShouldFire fires).
func LoadState(dataDir, sessionID string) (State, error) {
	b, err := os.ReadFile(StateFile(dataDir, sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("statushook: read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		// A corrupt state file is survivable: treat as fresh (fire), don't wedge.
		return State{}, nil
	}
	return s, nil
}

// Save persists the throttle state atomically (write-temp-then-rename).
func (s State) Save(dataDir, sessionID string) error {
	path := StateFile(dataDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("statushook: create state dir: %w", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("statushook: marshal state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("statushook: write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("statushook: rename state: %w", err)
	}
	return nil
}

// Sanitize makes a session id safe for a filename (mirrors attest's keying).
func Sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
}

package chat

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

// updateGolden lets `go test -update` rewrite the golden file when
// rendering changes intentionally. Off by default — diffs must
// surface in review.
var updateGolden = flag.Bool("update", false, "rewrite testdata/golden/* with current rendering")

// TestStandaloneRendersGolden verifies the standalone host produces
// the same end-to-end frame as the pre-refactor Model.View did:
// header line + rule, rounded stream box with selection indicator,
// rounded composer box, status bar with mode pill + key hints.
//
// Pre-refactor (before component-interface extraction) the
// equivalent rendering came from a single Model.View call. The
// post-refactor split moves chrome (header, status bar) to
// Standalone — this golden test guards the composition.
//
// Update with `go test ./pkg/tui/chat -update`.
func TestStandaloneRendersGolden(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		{Ts: t0, Actor: ActorUser, Text: "hi"},
		{
			Ts:        t0.Add(time.Second),
			FrameKind: sextantproto.FrameAssistantText,
			Body:      map[string]any{"text": "hello there"},
		},
		{
			Ts:        t0.Add(2 * time.Second),
			FrameKind: sextantproto.FrameToolCall,
			ToolName:  "read_file",
			Body:      map[string]any{"path": "a.go"},
		},
		{
			Ts:        t0.Add(3 * time.Second),
			FrameKind: sextantproto.FrameToolResult,
			ToolName:  "read_file",
			Body:      map[string]any{"bytes": float64(100)},
		},
	}
	m := New(Options{AgentName: "alice", Branch: "main"}).WithTurns(FramesToTurns(frames))
	s := NewStandalone(m)
	_, _ = s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got := s.View()

	goldenPath := filepath.Join("testdata", "golden", "standalone_default.txt")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	// Trim the trailing newline added by the writer; the View()
	// output doesn't include one.
	wantStr := strings.TrimSuffix(string(want), "\n")
	if got != wantStr {
		t.Errorf("standalone render diverged from golden — re-run with -update if intentional.\n--- got ---\n%s\n--- want ---\n%s", got, wantStr)
	}
}

// TestStandaloneTranslatesDoneMsgToQuit verifies the host swaps
// component.DoneMsg for tea.Quit so the chat reducer can emit the
// intent without knowing it's running standalone.
func TestStandaloneTranslatesDoneMsgToQuit(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"})
	s := NewStandalone(m)
	_, cmd := s.Update(component.DoneMsg{})
	if cmd == nil {
		t.Fatal("DoneMsg produced no cmd; want tea.Quit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("DoneMsg cmd produced %T, want tea.QuitMsg", msg)
	}
}

// TestModelSatisfiesComponentInterface is the compile-time +
// runtime smoke test that *chat.Model implements the full Tier 1
// contract. If the Component interface grows a method this fails
// to compile here.
func TestModelSatisfiesComponentInterface(t *testing.T) {
	t.Parallel()
	var _ component.Component = (*Model)(nil)
	// Light runtime check that the methods do what they say.
	m := New(Options{AgentName: "alice"})
	if m.Focused() {
		t.Error("freshly-constructed Model should not be focused")
	}
	if cmd := m.Focus(); cmd != nil {
		t.Logf("Focus returned a non-nil cmd (acceptable, but unusual): %v", cmd)
	}
	if !m.Focused() {
		t.Error("after Focus(), Focused() should be true")
	}
	m.Blur()
	if m.Focused() {
		t.Error("after Blur(), Focused() should be false")
	}
	m.SetSize(80, 20)
	if short := m.ShortHelp(); len(short) == 0 {
		t.Error("ShortHelp returned empty slice")
	}
	if full := m.FullHelp(); len(full) == 0 {
		t.Error("FullHelp returned empty slice")
	}
}

// TestStandaloneHeaderDotReflectsLifecycle pins feat-chat-tui-status-dot:
// the header carries a dot glyph and the model stores the last
// observed lifecycle so the chrome can color it. Color attribution
// itself is style-table lookup (see renderLifecycleDot) — verified by
// TestRenderLifecycleDotSelectsRoleByTransition below, which doesn't
// depend on the terminal color profile.
func TestStandaloneHeaderDotReflectsLifecycle(t *testing.T) {
	m := New(Options{AgentName: "alice"})
	s := NewStandalone(m)
	_, _ = s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	header := strings.SplitN(s.View(), "\n", 2)[0]
	if !strings.Contains(header, "●") {
		t.Fatalf("header missing the dot glyph: %q", header)
	}
	if m.hasLifecycle {
		t.Error("hasLifecycle = true before any envelope; want false")
	}

	m.Update(lifecycleMsg{Payload: sextantproto.LifecyclePayload{
		Transition: sextantproto.LifecycleEnded,
	}})
	if !m.hasLifecycle {
		t.Error("hasLifecycle = false after lifecycle envelope; want true")
	}
	if m.lastLifecycle.Transition != sextantproto.LifecycleEnded {
		t.Errorf("lastLifecycle.Transition = %q, want %q", m.lastLifecycle.Transition, sextantproto.LifecycleEnded)
	}
}

// TestRenderLifecycleDotSelectsRoleByTransition asserts on the style
// table directly: each lifecycle transition pulls a distinct style.
// Doesn't depend on the terminal color profile — compares the
// lipgloss.Style values returned by the dot picker.
func TestRenderLifecycleDotSelectsRoleByTransition(t *testing.T) {
	cases := []struct {
		name       string
		hasL       bool
		transition sextantproto.LifecycleEvent
		wantClass  string // success / attention / destructive / muted
	}{
		{"none", false, "", "muted"},
		{"started", true, sextantproto.LifecycleStarted, "success"},
		{"resumed", true, sextantproto.LifecycleResumedEvent, "success"},
		{"restarted", true, sextantproto.LifecycleRestartedEvent, "success"},
		{"turn_ended", true, sextantproto.LifecycleTurnEnded, "success"},
		{"paused", true, sextantproto.LifecyclePausedEvent, "attention"},
		{"archived", true, sextantproto.LifecycleArchivedEvent, "attention"},
		{"ended", true, sextantproto.LifecycleEnded, "destructive"},
		{"crashed", true, sextantproto.LifecycleCrashedEvent, "destructive"},
		{"lost", true, sextantproto.LifecycleLostEvent, "lost"},
		{"unknown", true, sextantproto.LifecycleEvent("future"), "muted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(Options{AgentName: "alice"})
			m.hasLifecycle = tc.hasL
			m.lastLifecycle.Transition = tc.transition
			s := &Standalone{inner: m}
			got := s.lifecycleDotRoleClass()
			if got != tc.wantClass {
				t.Errorf("lifecycleDotRoleClass = %q, want %q", got, tc.wantClass)
			}
		})
	}
}

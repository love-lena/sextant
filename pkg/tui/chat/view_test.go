package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func TestChatTUIRendersTurnsAndAttachesToolCalls(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 34, 56, 0, time.UTC)
	frames := []Frame{
		{Ts: t0, Actor: ActorUser, Text: "read main.go"},
		envAt(t0.Add(time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "on it"}, ""),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolCall, map[string]any{"path": "main.go"}, "read_file"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameToolResult, map[string]any{"bytes": float64(120)}, "read_file"),
		envAt(t0.Add(4*time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "120 bytes"}, ""),
	}
	m := New(Options{AgentName: "alice"}).WithTurns(FramesToTurns(frames))
	m.focus = FocusStream // render with stream selection visible (▌ bar)
	m = mWithSize(m, 80, 24)
	out := m.View()

	if !strings.Contains(out, "read main.go") {
		t.Errorf("user turn missing: %q", out)
	}
	if !strings.Contains(out, "on it") {
		t.Errorf("agent turn 1 missing: %q", out)
	}
	if !strings.Contains(out, "120 bytes") {
		t.Errorf("agent turn 2 missing: %q", out)
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("tool call missing: %q", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("header missing agent name: %q", out)
	}
	lastIdx := strings.Index(out, "120 bytes")
	if lastIdx < 0 {
		t.Fatalf("can't find last turn in output")
	}
	if !strings.Contains(out[:lastIdx], "▌") {
		t.Errorf("selection mark glyph not present before last turn")
	}
}

func TestViewHidesComposerInReadMode(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice", Read: true}).WithTurns(seedTurns())
	m = mWithSize(m, 80, 24)
	out := m.View()
	if strings.Contains(out, "i to edit") {
		t.Errorf("read mode should not show composer hint: %q", out)
	}
	if !strings.Contains(out, "READ") {
		t.Errorf("read mode pill missing: %q", out)
	}
}

// mWithSize is a test helper that drives a WindowSizeMsg into the model
// so View has dimensions to work with. Returns the new Model.
func mWithSize(m Model, w, h int) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func TestStreamClipsToHeightBudget(t *testing.T) {
	t.Parallel()
	// 30 short turns + small height budget. We expect the rendered
	// output to be clipped — total lines should not exceed the budget.
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	turns := make([]Turn, 30)
	for i := range turns {
		turns[i] = Turn{Ts: t0.Add(time.Duration(i) * time.Second), Actor: ActorAgent, Text: "row"}
	}
	m := New(Options{AgentName: "alice"}).WithTurns(turns)
	// Total height = 30 (chrome reserves 9 for non-read mode → streamHeight = 21)
	m = mWithSize(m, 100, 30)

	rendered := m.renderStream(100)
	lines := strings.Split(rendered, "\n")
	if len(lines) > 21 {
		t.Errorf("rendered exceeds height budget: %d lines, budget 21", len(lines))
	}
}

func TestStreamSelectionCenteredInWindow(t *testing.T) {
	t.Parallel()
	// 30 turns, selection in the middle. The selected turn's line should
	// fall inside the clipped window.
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	turns := make([]Turn, 30)
	for i := range turns {
		turns[i] = Turn{Ts: t0.Add(time.Duration(i) * time.Second), Actor: ActorAgent, Text: fmt.Sprintf("turn-%d", i)}
	}
	m := New(Options{AgentName: "alice"}).WithTurns(turns)
	m.focus = FocusStream // set explicitly so k presses decrement from the start
	m = mWithSize(m, 100, 30)

	// Move selection to turn 15 by simulating keys.
	for i := 0; i < (29 - 15); i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		m = next.(Model)
	}
	if m.Selection() != 15 {
		t.Fatalf("selection setup: want 15, got %d", m.Selection())
	}

	rendered := m.renderStream(100)
	// The selected turn's text ("turn-15") should appear in the clipped
	// output.
	if !strings.Contains(rendered, "turn-15") {
		t.Errorf("selected turn not in clipped window: %q", rendered)
	}
	// Turns far from selection should NOT appear.
	if strings.Contains(rendered, "turn-0:") {
		t.Errorf("first turn appeared in window centered on turn 15: %q", rendered)
	}
}

func TestStreamClampsAtTopWhenSelectionNearStart(t *testing.T) {
	t.Parallel()
	// Selection at index 1 (near top). Window should clamp at line 0 —
	// turn-0 must be visible, no negative offset.
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	turns := make([]Turn, 30)
	for i := range turns {
		turns[i] = Turn{Ts: t0.Add(time.Duration(i) * time.Second), Actor: ActorAgent, Text: fmt.Sprintf("turn-%d", i)}
	}
	m := New(Options{AgentName: "alice"}).WithTurns(turns)
	m.focus = FocusStream // set explicitly so k presses decrement from the start
	m = mWithSize(m, 100, 30)
	// Step selection up to index 1.
	for i := 0; i < 28; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
		m = next.(Model)
	}
	if m.Selection() != 1 {
		t.Fatalf("selection setup: want 1, got %d", m.Selection())
	}

	rendered := m.renderStream(100)
	if !strings.Contains(rendered, "turn-0") {
		t.Errorf("top turn missing when selection near start: %q", rendered)
	}
}

func TestChatTUIToolCallStatusColor(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "running"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{"path": "a"}, "good_tool"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolResult, map[string]any{"ok": true}, "good_tool"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameToolCall, map[string]any{"path": "b"}, "bad_tool"),
		envAt(t0.Add(4*time.Second), sextantproto.FrameToolResult, map[string]any{"error": "boom"}, "bad_tool"),
	}
	m := New(Options{AgentName: "alice"}).WithTurns(FramesToTurns(frames))
	// Move selection off the agent turn so the tool lines render WITHOUT
	// selection-bg propagation — we want to assert the bare Success /
	// Destructive role-token output.
	m.selection = -1 // sentinel: no turn selected, plain rendering
	m = mWithSize(m, 100, 24)

	styles := defaultStyles()
	okStr := styles.Success.Render("ok")
	failStr := styles.Destructive.Render("failed")

	out := m.View()
	if !strings.Contains(out, okStr) {
		t.Errorf("ok tool call missing success-styled token: rendered=%q", out)
	}
	if !strings.Contains(out, failStr) {
		t.Errorf("failed tool call missing destructive-styled token: rendered=%q", out)
	}
}

func TestRenderTurnPreservesInternalWhitespace(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	// An ASCII-art-style turn with deliberate internal spacing. The
	// renderer must preserve every space inside lines that fit the
	// width budget.
	turn := Turn{
		Ts:    t0,
		Actor: ActorAgent,
		Text:  "|0........1.........2.........3.........4|\n|         X         X         X         X|",
	}
	m := New(Options{AgentName: "alice"}).WithTurns([]Turn{turn})
	m.focus = FocusStream
	m.selection = -1 // no turn highlighted, plain rendering
	m = mWithSize(m, 120, 30)

	out := m.View()
	// Verify the spaces between X's were not collapsed. The runner-
	// style alignment "X         X" (X + 9 spaces + X) must survive.
	if !strings.Contains(out, "X         X") {
		t.Errorf("internal whitespace collapsed; output missing 'X         X':\n%s", out)
	}
	// Same for the ruler line.
	if !strings.Contains(out, "|0........1.........2.........3.........4|") {
		t.Errorf("ruler line lost or mangled:\n%s", out)
	}
}

func TestStreamGluesToBottomWhenContentFits(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	turns := []Turn{
		{Ts: t0, Actor: ActorAgent, Text: "hello"},
		{Ts: t0.Add(time.Second), Actor: ActorAgent, Text: "world"},
	}
	m := New(Options{AgentName: "alice"}).WithTurns(turns)
	// Height 30 → streamHeight = 21 (non-read mode: 30 - 9 chrome rows).
	m = mWithSize(m, 100, 30)

	rendered := m.renderStream(100)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 21 {
		t.Errorf("expected exactly streamHeight=21 lines (padded), got %d", len(lines))
	}
	// First line should be blank (top-padding).
	if lines[0] != "" {
		t.Errorf("first line should be empty pad, got %q", lines[0])
	}
	// Content must land in the BOTTOM portion of the viewport.
	bottomHalf := strings.Join(lines[len(lines)-5:], "\n")
	if !strings.Contains(bottomHalf, "hello") {
		t.Errorf("'hello' should be in bottom rows, got: %q", bottomHalf)
	}
	if !strings.Contains(bottomHalf, "world") {
		t.Errorf("'world' should be in bottom rows, got: %q", bottomHalf)
	}
}

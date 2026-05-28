package chat

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func seedTurns() []Turn {
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	return []Turn{
		{Ts: t0, Actor: ActorUser, Text: "hi"},
		{Ts: t0.Add(time.Second), Actor: ActorAgent, Text: "hello"},
		{Ts: t0.Add(2 * time.Second), Actor: ActorUser, Text: "next"},
		{Ts: t0.Add(3 * time.Second), Actor: ActorAgent, Text: "ok"},
	}
}

func mkKey(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// applyKey is a test helper: feed a key to the model and return the new model.
func applyKey(t *testing.T, m *Model, k tea.KeyMsg) *Model {
	t.Helper()
	next, _ := m.Update(k)
	mm, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned non-*Model: %T", next)
	}
	return mm
}

func TestModelInitDefaultsToNormalOnComposer(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"})
	m = m.WithTurns(seedTurns())
	if m.Mode() != ModeNormal {
		t.Errorf("mode: want NORMAL, got %v", m.Mode())
	}
	if m.FocusArea() != FocusComposer {
		t.Errorf("focus: want FocusComposer, got %v", m.FocusArea())
	}
}

func TestNormalJK_StepsSelection(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	// Default is FocusComposer; first k moves to FocusStream at last turn.
	m = applyKey(t, m, mkKey("k")) // FocusComposer → FocusStream, sel=3 (last)
	if m.FocusArea() != FocusStream {
		t.Fatalf("setup: want FocusStream after first k, got %v", m.FocusArea())
	}
	m = applyKey(t, m, mkKey("k")) // sel 3→2
	if got := m.Selection(); got != 2 {
		t.Errorf("after k: want 2, got %d", got)
	}
	m = applyKey(t, m, mkKey("k"))
	m = applyKey(t, m, mkKey("k"))
	if got := m.Selection(); got != 0 {
		t.Errorf("after 2xk more: want 0, got %d", got)
	}
	m = applyKey(t, m, mkKey("k"))
	if got := m.Selection(); got != 0 {
		t.Errorf("k clamps at 0: got %d", got)
	}
	m = applyKey(t, m, mkKey("j"))
	if got := m.Selection(); got != 1 {
		t.Errorf("after j: want 1, got %d", got)
	}
}

func TestNormalJ_OnLastTurnEntersFocusComposer(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, mkKey("k")) // FocusComposer → FocusStream, sel=last
	if m.FocusArea() != FocusStream {
		t.Fatalf("setup: focus should be stream, got %v", m.FocusArea())
	}
	m = applyKey(t, m, mkKey("j")) // j past last → composer
	if m.FocusArea() != FocusComposer {
		t.Errorf("after j past last: want FocusComposer, got %v", m.FocusArea())
	}
}

func TestNormalK_OnComposerEntersStreamAtLast(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	// default: FocusComposer
	m = applyKey(t, m, mkKey("k"))
	if m.FocusArea() != FocusStream {
		t.Errorf("after k from composer: want FocusStream, got %v", m.FocusArea())
	}
	if m.Selection() != len(seedTurns())-1 {
		t.Errorf("selection: want %d, got %d", len(seedTurns())-1, m.Selection())
	}
}

func TestNormalGgG_JumpsTopBottom(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, mkKey("g"))
	m = applyKey(t, m, mkKey("g"))
	if got := m.Selection(); got != 0 {
		t.Errorf("gg: want 0, got %d", got)
	}
	m = applyKey(t, m, mkKey("G"))
	if got := m.Selection(); got != 3 {
		t.Errorf("G: want 3, got %d", got)
	}
}

func TestNormalI_EntersInsertMode(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, mkKey("i"))
	if m.Mode() != ModeInsert {
		t.Errorf("after i: want INSERT, got %v", m.Mode())
	}
}

func TestInsertEsc_ReturnsToNormalPreservingDraft(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	// Default focus is FocusComposer; i from there snapshots it.
	m = applyKey(t, m, mkKey("i"))
	for _, r := range "hold on" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = applyKey(t, m, mkKey("esc"))
	if m.Mode() != ModeNormal {
		t.Errorf("after esc: want NORMAL, got %v", m.Mode())
	}
	if got := m.Draft(); got != "hold on" {
		t.Errorf("draft preserved: want %q, got %q", "hold on", got)
	}
	// Esc restores the snapshot: came from FocusComposer → returns there.
	if m.FocusArea() != FocusComposer {
		t.Errorf("after esc with no prior turn navigation: want FocusComposer, got %v", m.FocusArea())
	}
	// Re-enter INSERT and append more
	m = applyKey(t, m, mkKey("i"))
	for _, r := range " — wait" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Draft(); got != "hold on — wait" {
		t.Errorf("draft appended: want %q, got %q", "hold on — wait", got)
	}
}

func TestEscRestoresFocusStreamWhenPriorWasStream(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, mkKey("k")) // FocusComposer → FocusStream, sel=last
	m = applyKey(t, m, mkKey("k")) // sel=last-1
	priorSel := m.Selection()
	m = applyKey(t, m, mkKey("i"))
	for _, r := range "test" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = applyKey(t, m, mkKey("esc"))
	if m.FocusArea() != FocusStream {
		t.Errorf("focus: want FocusStream, got %v", m.FocusArea())
	}
	if m.Selection() != priorSel {
		t.Errorf("selection: want %d (restored), got %d", priorSel, m.Selection())
	}
}

func TestInsertJ_TypesCharacterNotNavigation(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	selBefore := m.Selection()
	m = applyKey(t, m, mkKey("i"))
	m = applyKey(t, m, mkKey("j"))
	if got := m.Draft(); got != "j" {
		t.Errorf("draft after j in INSERT: want %q, got %q", "j", got)
	}
	if m.Selection() != selBefore {
		t.Errorf("selection moved in INSERT: was %d, now %d", selBefore, m.Selection())
	}
}

func TestInsertEnter_SendsDraftAndBouncesToNormal(t *testing.T) {
	t.Parallel()
	var sent []string
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = m.WithSendHook(func(text string) { sent = append(sent, text) })
	m = applyKey(t, m, mkKey("i"))
	for _, r := range "hello" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = applyKey(t, m, mkKey("enter"))
	if len(sent) != 1 || sent[0] != "hello" {
		t.Errorf("sent: want [hello], got %v", sent)
	}
	if m.Mode() != ModeNormal {
		t.Errorf("after enter: want NORMAL, got %v", m.Mode())
	}
	if got := m.Draft(); got != "" {
		t.Errorf("draft after send: want empty, got %q", got)
	}
	turns := m.Turns()
	if len(turns) != len(seedTurns())+1 {
		t.Fatalf("turn count: want %d, got %d", len(seedTurns())+1, len(turns))
	}
	last := turns[len(turns)-1]
	if last.Actor != ActorUser || last.Text != "hello" {
		t.Errorf("last turn: %+v", last)
	}
	if m.Selection() != len(turns)-1 {
		t.Errorf("selection: want %d, got %d", len(turns)-1, m.Selection())
	}
	if m.FocusArea() != FocusStream {
		t.Errorf("after send: want FocusStream, got %v", m.FocusArea())
	}
}

func TestInsertEnter_EmptyDraftIsNoop(t *testing.T) {
	t.Parallel()
	var sent []string
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = m.WithSendHook(func(text string) { sent = append(sent, text) })
	m = applyKey(t, m, mkKey("i"))
	m = applyKey(t, m, mkKey("enter"))
	if len(sent) != 0 {
		t.Errorf("empty draft sent: %v", sent)
	}
	if m.Mode() != ModeInsert {
		t.Errorf("empty enter stayed in INSERT? mode=%v", m.Mode())
	}
}

func TestChatLostStateDisablesInput(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alpha"})
	if m.inputDisabled() {
		t.Error("inputDisabled returned true before any lifecycle envelope")
	}
	m.Update(lifecycleMsg{Payload: sextantproto.LifecyclePayload{
		Transition: sextantproto.LifecycleLostEvent,
	}})
	if !m.inputDisabled() {
		t.Error("inputDisabled returned false after lost envelope")
	}
	m.Update(lifecycleMsg{Payload: sextantproto.LifecyclePayload{
		Transition: sextantproto.LifecycleRestartedEvent,
	}})
	if m.inputDisabled() {
		t.Error("inputDisabled returned true after restarted envelope (should re-enable)")
	}
}

func TestChatLostStateBindsRToRestart(t *testing.T) {
	t.Parallel()
	agentID := uuid.New()
	m := New(Options{AgentName: "alpha", AgentID: agentID})
	m.Update(lifecycleMsg{Payload: sextantproto.LifecyclePayload{
		Transition: sextantproto.LifecycleLostEvent,
	}})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd == nil {
		t.Fatal("R key produced no command in lost state")
	}
	msg := cmd()
	req, ok := msg.(RestartRequestedMsg)
	if !ok {
		t.Fatalf("cmd msg type = %T, want RestartRequestedMsg", msg)
	}
	if req.AgentID != agentID {
		t.Errorf("RestartRequestedMsg.AgentID = %s, want %s", req.AgentID, agentID)
	}
}

func TestChatLostStateBlocksInsert(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alpha"})
	m.Update(lifecycleMsg{Payload: sextantproto.LifecyclePayload{
		Transition: sextantproto.LifecycleLostEvent,
	}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if m.Mode() == ModeInsert {
		t.Error("entered INSERT mode while lost; should be blocked")
	}
}

func TestChatTUIReadModeDisallowsInsert(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice", Read: true}).WithTurns(seedTurns())
	m = mWithSize(m, 80, 24)
	// 'i' must be a no-op in read mode.
	m = applyKey(t, m, mkKey("i"))
	if m.Mode() != ModeNormal {
		t.Errorf("read mode after i: want NORMAL, got %v", m.Mode())
	}
	// READ pill lives in the host's status bar — render via Standalone.
	out := renderStandalone(m, 80, 24)
	if !strings.Contains(out, "READ") {
		t.Errorf("status bar missing READ pill: %q", out)
	}
	// In read mode the composer placeholder hint shouldn't render —
	// renderComposerBox is short-circuited by View when Read=true. The
	// textarea's placeholder ("press i to compose…") must not appear.
	if strings.Contains(out, "press i to compose") {
		t.Errorf("composer placeholder leaked in read mode: %q", out)
	}
}

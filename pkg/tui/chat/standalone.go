package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/tui/component"
)

// HostChromeReserved is the number of rows the standalone wrapper
// adds around the chat component's content rect:
//
//	1 row — header name + branch
//	1 row — thin rule below the header
//	1 row — blank gap before the status bar (legacy spacing)
//	1 row — status bar
//	------
//	4 rows
//
// Inside its content rect the component reserves further rows for
// the stream and composer pane borders — see Model.SetSize.
//
// Exposed because test helpers (mWithSize in view_test.go) need to
// simulate the same outer-chrome reservation to reproduce
// pre-refactor render dimensions.
const HostChromeReserved = 4

// Standalone wraps a *Model with the chrome (header, status bar)
// that the dash does not draw. Implements tea.Model so it can be
// passed straight to tea.NewProgram. Translates component.DoneMsg
// into tea.Quit.
//
// Pre-refactor the equivalent of this code lived in Model.View;
// moving it here matches `conventions/tui-conventions.md` §
// "Component contract → Chrome lives outside the component" and
// frees the dash to draw its own chrome around the same component.
type Standalone struct {
	host  *component.Host
	inner *Model
}

// NewStandalone wraps m for standalone use. Caller is expected to
// pass the returned *Standalone to tea.NewProgram.
//
// The wrapper also calls Focus() on the inner component at startup
// (a standalone surface is always focused) so the component-level
// focused bit is set before any rendering happens.
func NewStandalone(m *Model) *Standalone {
	s := &Standalone{inner: m}
	s.host = component.NewHost(
		m,
		component.WithChrome(s.renderChrome, HostChromeReserved),
		component.WithInitialFocus(),
	)
	return s
}

// Init wires the inner component's Init + initial focus.
func (s *Standalone) Init() tea.Cmd { return s.host.Init() }

// Update routes through the host (which translates DoneMsg →
// tea.Quit and forwards WindowSizeMsg → SetSize on the inner).
// RestartRequestedMsg is intercepted here and dispatched to the
// inner model's restart hook (wired by program.go against the Bus).
func (s *Standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if req, ok := msg.(RestartRequestedMsg); ok {
		if fn := s.inner.restart; fn != nil {
			fn(req.AgentID)
		}
		return s, nil
	}
	_, cmd := s.host.Update(msg)
	return s, cmd
}

// View composes the component's content area with the surrounding
// chrome (header + status bar).
func (s *Standalone) View() string { return s.host.View() }

// Inner returns the wrapped *Model. Exposed for tests that need to
// drive state directly without going through Update (e.g. seed
// turns and assert on rendered output).
func (s *Standalone) Inner() *Model { return s.inner }

// renderChrome is the ChromeFunc bound by NewStandalone. Receives
// the full terminal width/height (height is *terminal* height, not
// content-rect height — the host's chromeReserved was already
// subtracted before SetSize on the component) and the
// component-rendered content.
func (s *Standalone) renderChrome(width, _ int, content string) string {
	if width <= 0 {
		width = 80
	}
	header := s.renderHeader(width)
	status := s.renderStatusBar(width)
	// Layout (top to bottom):
	//   header line + thin rule below   (2 rows)
	//   component content area          (variable)
	//   blank gap                       (1 row)
	//   status bar                      (1 row)
	return strings.Join([]string{header, content, "", status}, "\n")
}

// renderHeader draws the lifecycle status dot + agent name + optional
// branch + thin rule. The dot reflects the most recent lifecycle
// envelope (feat-chat-tui-status-dot):
//
//	green  — started / resumed / restarted / turn_ended
//	yellow — paused / archived
//	red    — ended / crashed
//	muted  — no lifecycle envelope seen yet
//
// Moved here from view.go: pre-refactor it lived on Model.View; per
// the Component contract the host owns chrome.
func (s *Standalone) renderHeader(width int) string {
	m := s.inner
	dot := s.renderLifecycleDot()
	name := m.styles.HeaderName.Render(m.opts.AgentName)
	line := dot + " " + name
	if m.opts.Branch != "" {
		line += "  " + m.styles.HeaderBranch.Render("⎇ "+m.opts.Branch)
	}
	rule := m.styles.HeaderRule.Render(strings.Repeat("─", width))
	return line + "\n" + rule
}

// renderLifecycleDot paints a single dot glyph in the role tone that
// matches the inner Model's last lifecycle envelope. See renderHeader
// for the color mapping. Falls back to a muted dot when no envelope
// has been observed.
func (s *Standalone) renderLifecycleDot() string {
	const dot = "●"
	m := s.inner
	switch s.lifecycleDotRoleClass() {
	case "success":
		return m.styles.Success.Render(dot)
	case "attention":
		return m.styles.Attention.Render(dot)
	case "destructive":
		return m.styles.Destructive.Render(dot)
	case "lost":
		return m.styles.Lost.Render(dot)
	default:
		return m.styles.Muted.Render(dot)
	}
}

// lifecycleDotRoleClass returns the role-class name driving the
// header dot's color. Split from renderLifecycleDot so tests can
// assert on the mapping without depending on the terminal's color
// profile (lipgloss renders styles as plain text under no-color).
func (s *Standalone) lifecycleDotRoleClass() string {
	m := s.inner
	if !m.hasLifecycle {
		return "muted"
	}
	switch m.lastLifecycle.Transition {
	case "started", "resumed", "restarted", "turn_ended":
		return "success"
	case "paused", "archived":
		return "attention"
	case "ended", "crashed":
		return "destructive"
	case "lost":
		return "lost"
	default:
		return "muted"
	}
}

// renderStatusBar is the bottom-of-screen strip outside any pane.
// Shows the mode pill on the left and active-mode key hints on
// the right.
//
// Spec §"Mode-aware status bar": only the keys that work in the
// current mode appear — no busy legend of inert hotkeys. The mode
// is read from the inner component each render.
func (s *Standalone) renderStatusBar(width int) string {
	m := s.inner
	var pill string
	switch {
	case m.opts.Read:
		pill = m.styles.StatusRead.Render(" READ ")
	case m.mode == ModeInsert:
		pill = m.styles.StatusInsert.Render("INSERT")
	default:
		pill = m.styles.StatusNormal.Render("NORMAL")
	}

	var hints []string
	switch {
	case m.opts.Read:
		hints = s.modeHints("read")
	case m.mode == ModeInsert:
		hints = s.modeHints("insert")
	default:
		hints = s.modeHints("normal")
	}
	hintStr := strings.Join(hints, "   ")

	left := " " + pill + "  "
	right := hintStr
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// modeHints returns the active-mode key chips. Moved from view.go
// because the chips render in the host-owned status bar.
func (s *Standalone) modeHints(mode string) []string {
	m := s.inner
	chip := func(key, desc string) string {
		return m.styles.KeyHintKey.Render(key) + " " + m.styles.KeyHintDesc.Render(desc)
	}
	switch mode {
	case "insert":
		return []string{chip("↵", "send"), chip("⇧↵", "newline"), chip("Esc", "back")}
	case "read":
		return []string{chip("j/k", "step"), chip("gg/G", "top·bot"), chip("q", "quit")}
	default: // normal
		return []string{chip("j/k", "step"), chip("gg/G", "top·bot"), chip("i", "edit"), chip("q", "quit")}
	}
}

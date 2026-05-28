package chat

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/sextantproto"
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
//
// Note: the restart-error banner (rendered when Model.lastError is
// non-empty) is overlaid in place of the blank-gap row before the
// status bar; it does NOT reserve an extra row. That keeps the
// content rect dimensions stable across error states so the stream
// pane doesn't jiggle when the banner appears/disappears.
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
// The hook returns a tea.Cmd that runs the RPC and emits
// restartFailedMsg on failure; threading that cmd back into bubbletea
// is what surfaces the error banner via the model's Update.
func (s *Standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if req, ok := msg.(RestartRequestedMsg); ok {
		if fn := s.inner.restart; fn != nil {
			return s, fn(req.AgentID)
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
//
// When Model.lastError is non-empty, an error banner replaces the
// blank-gap row before the status bar so the row count (and therefore
// the component's content rect) stays constant — see HostChromeReserved.
func (s *Standalone) renderChrome(width, _ int, content string) string {
	if width <= 0 {
		width = 80
	}
	header := s.renderHeader(width)
	status := s.renderStatusBar(width)
	gap := ""
	if banner := s.renderRestartErrorBanner(width); banner != "" {
		gap = banner
	}
	// Layout (top to bottom):
	//   header line + thin rule below            (2 rows)
	//   component content area                   (variable)
	//   blank gap OR error banner (mutually exclusive, 1 row)
	//   status bar                               (1 row)
	return strings.Join([]string{header, content, gap, status}, "\n")
}

// renderRestartErrorBanner returns the inline banner for the most
// recent restart_agent failure, or "" if none is active. Rendered in
// the destructive role tone so it reads as a dismissable error, not
// a steady-state warning. Width-aware: the message is truncated with
// an ellipsis if it would overflow the row.
//
// The banner replaces the blank-gap row above the status bar (see
// renderChrome) so it doesn't change the content rect's height.
func (s *Standalone) renderRestartErrorBanner(width int) string {
	m := s.inner
	if m.lastError == "" {
		return ""
	}
	prefix := " restart failed: "
	suffix := "  (press R to retry)"
	avail := width - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	if avail < 1 {
		avail = 1
	}
	msg := m.lastError
	if lipgloss.Width(msg) > avail {
		// Truncate at rune boundary, leave room for the ellipsis.
		runes := []rune(msg)
		if avail > 1 {
			msg = string(runes[:avail-1]) + "…"
		} else {
			msg = "…"
		}
	}
	return m.styles.Destructive.Render(prefix + msg + suffix)
}

// renderHeader draws the lifecycle status dot + agent name + lifecycle
// state word + optional branch + thin rule. The dot reflects the most
// recent lifecycle envelope (feat-chat-tui-status-dot):
//
//	green  — started / resumed / restarted / turn_ended
//	yellow — paused / archived
//	red    — ended / crashed
//	muted  — no lifecycle envelope seen yet
//
// The state word ("running", "ended", "lost", …) is rendered next to
// the name (feat-tui-chat-header-name-and-lifecycle) and shares the
// dot's role-class color so the two read as a single signal. Terminal
// states append a relative-time suffix sourced from the envelope's
// wire timestamp (`ended (12m ago)`).
//
// Moved here from view.go: pre-refactor it lived on Model.View; per
// the Component contract the host owns chrome.
func (s *Standalone) renderHeader(width int) string {
	m := s.inner
	dot := s.renderLifecycleDot()
	name := m.styles.HeaderName.Render(m.opts.AgentName)
	line := dot + " " + name
	if state := s.renderLifecycleStateWord(); state != "" {
		line += " " + m.styles.Muted.Render("·") + " " + state
	}
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

// lifecycleStateWord returns the plain-text state word displayed in
// the header next to the agent name. Sourced from
// `m.lastLifecycle.State`, falling back to `Transition` when State is
// empty (matching how the dot color picker tolerates a missing field).
// Returns empty when no envelope has been seen yet — the header omits
// the `· <state>` segment in that case.
func (s *Standalone) lifecycleStateWord() string {
	m := s.inner
	if !m.hasLifecycle {
		return ""
	}
	if word := string(m.lastLifecycle.State); word != "" {
		return word
	}
	return string(m.lastLifecycle.Transition)
}

// renderLifecycleStateWord paints the state word in the same role-
// class color as the dot, with a relative-time suffix on terminal
// transitions (`ended (12m ago)`, `lost (just now)`). Returns the
// styled segment ready to drop into the header line, or empty string
// when no envelope has been seen.
func (s *Standalone) renderLifecycleStateWord() string {
	m := s.inner
	word := s.lifecycleStateWord()
	if word == "" {
		return ""
	}
	text := word
	if isTerminalLifecycleTransition(m.lastLifecycle.Transition) {
		if rel := relativeTimeAgo(m.lastLifecycleTs, time.Now()); rel != "" {
			text = word + " (" + rel + ")"
		}
	}
	var style lipgloss.Style
	switch s.lifecycleDotRoleClass() {
	case "success":
		style = m.styles.Success
	case "attention":
		style = m.styles.Attention
	case "destructive":
		style = m.styles.Destructive
	case "lost":
		style = m.styles.Lost
	default:
		style = m.styles.Muted
	}
	return style.Render(text)
}

// isTerminalLifecycleTransition reports whether a transition leaves
// the agent in a non-running state that warrants a "how long ago"
// annotation. `archived` and `ended` are clean stops; `crashed` and
// `lost` are failure modes — all four read more clearly with a
// relative timestamp.
func isTerminalLifecycleTransition(t sextantproto.LifecycleEvent) bool {
	switch t {
	case sextantproto.LifecycleEnded,
		sextantproto.LifecycleCrashedEvent,
		sextantproto.LifecycleLostEvent,
		sextantproto.LifecycleArchivedEvent:
		return true
	}
	return false
}

// relativeTimeAgo formats `now - ts` as a short human string ("just
// now", "12m ago", "3h ago", "2d ago"). Returns empty when ts is the
// zero value (caller should omit the suffix entirely in that case).
// Future timestamps fall through to "just now" — clock skew is more
// common than time-travel.
func relativeTimeAgo(ts, now time.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := now.Sub(ts)
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
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

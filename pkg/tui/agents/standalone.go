package agents

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/tui/component"
)

// HostChromeReserved is the number of rows the standalone wrapper
// adds around the agents component's content rect:
//
//	1 row — title line ("sextant agents  operator=…")
//	1 row — blank gap
//	1 row — blank gap before the status bar
//	1 row — status bar
//	------
//	4 rows
//
// The pre-refactor model rendered title + status inline; moving them
// out to the wrapper matches `conventions/tui-conventions.md` §
// "Component contract → Chrome lives outside the component" and lets
// the dash draw its own chrome around the same model.
const HostChromeReserved = 4

// Standalone wraps a *Model with the chrome (title, status bar) that
// the dash does not draw. Implements tea.Model so it can be passed
// straight to tea.NewProgram. Translates component.DoneMsg into
// tea.Quit.
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

// NewStandaloneWithInitialLoad is NewStandalone with an additional
// `LoadMsg{ID: id}` fired during Init. Used by
// `sextant agents show <id> -i` to seed the cursor on the requested
// agent.
func NewStandaloneWithInitialLoad(m *Model, id string) *Standalone {
	s := &Standalone{inner: m}
	s.host = component.NewHost(
		m,
		component.WithChrome(s.renderChrome, HostChromeReserved),
		component.WithInitialFocus(),
		component.WithInitialLoad(id),
	)
	return s
}

// Init wires the inner component's Init + initial focus.
func (s *Standalone) Init() tea.Cmd { return s.host.Init() }

// Update routes through the host (which translates DoneMsg →
// tea.Quit and forwards WindowSizeMsg → SetSize on the inner).
func (s *Standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := s.host.Update(msg)
	return s, cmd
}

// View composes the component's content area with the surrounding
// chrome (title + status bar).
func (s *Standalone) View() string { return s.host.View() }

// Inner returns the wrapped *Model. Exposed for tests that need to
// drive state directly without going through Update.
func (s *Standalone) Inner() *Model { return s.inner }

// renderChrome is the ChromeFunc bound by NewStandalone. Receives
// the full terminal width/height and the component-rendered content.
func (s *Standalone) renderChrome(width, _ int, content string) string {
	if width <= 0 {
		width = 80
	}
	m := s.inner
	th := m.styles()
	title := th.title.Render("sextant agents") + "  " +
		th.muted.Render(fmt.Sprintf("operator=%s", m.operator))
	status := s.renderStatusBar(width)
	// Layout (top to bottom):
	//   title line                               (1 row)
	//   blank gap                                (1 row)
	//   component content area                   (variable)
	//   blank gap                                (1 row)
	//   status bar                               (1 row)
	return strings.Join([]string{title, "", content, "", status}, "\n")
}

func (s *Standalone) renderStatusBar(width int) string {
	m := s.inner
	th := m.styles()
	left := fmt.Sprintf("agents=%d  selected=%s", len(m.agents), AbbrSelected(m.selected))
	if !m.refreshed.IsZero() {
		left += "  refreshed=" + m.refreshed.Format("15:04:05")
	}
	right := fmt.Sprintf("pending=%d  [j/k] nav  [Enter] select  [r] reload  [?] help  [q] quit", m.pending)
	gap := strings.Repeat(" ", MaxGap(width, lipgloss.Width(left), lipgloss.Width(right)))
	return th.status.Render(left + gap + right)
}

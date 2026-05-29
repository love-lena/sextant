package agentdetail

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/theme"
	"github.com/love-lena/sextant/pkg/tui/component"
)

// HostChromeReserved: title(1) + gap(1) + gap(1) + status(1) = 4 rows.
const HostChromeReserved = 4

// Standalone wraps a *Model with title + status chrome for
// `sextant agents show <id> -i`.
type Standalone struct {
	host  *component.Host
	inner *Model
	th    theme.Theme
}

// NewStandalone wraps m and fires an initial LoadMsg for agentID.
func NewStandalone(m *Model, agentID string) *Standalone {
	s := &Standalone{inner: m, th: theme.DefaultTheme()}
	opts := []component.HostOption{
		component.WithChrome(s.renderChrome, HostChromeReserved),
		component.WithInitialFocus(),
	}
	if agentID != "" {
		opts = append(opts, component.WithInitialLoad(agentID))
	}
	s.host = component.NewHost(m, opts...)
	return s
}

func (s *Standalone) Init() tea.Cmd { return s.host.Init() }

func (s *Standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := s.host.Update(msg)
	return s, cmd
}

func (s *Standalone) View() string  { return s.host.View() }
func (s *Standalone) Inner() *Model { return s.inner }

func (s *Standalone) renderChrome(width, _ int, content string) string {
	if width <= 0 {
		width = 80
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(s.th.Accent).Render("sextant agent")
	status := s.renderStatus(width)
	return strings.Join([]string{title, "", content, "", status}, "\n")
}

func (s *Standalone) renderStatus(width int) string {
	st := lipgloss.NewStyle().Foreground(s.th.ForegroundMuted)
	left := fmt.Sprintf("agent=%s", shortRef(s.inner.AgentID()))
	right := "[j/k] scroll  [r] refresh  [q] quit"
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return st.Render(left + strings.Repeat(" ", gap) + right)
}

func shortRef(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

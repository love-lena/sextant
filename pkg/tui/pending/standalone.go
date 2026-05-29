package pending

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

// Standalone wraps a *Model with title + status-bar chrome for standalone
// use. The dash mounts the bare Model with its own chrome instead.
type Standalone struct {
	host  *component.Host
	inner *Model
	th    theme.Theme
}

// NewStandalone wraps m for `sextant pending list -i`.
func NewStandalone(m *Model) *Standalone {
	s := &Standalone{inner: m, th: theme.DefaultTheme()}
	s.host = component.NewHost(m,
		component.WithChrome(s.renderChrome, HostChromeReserved),
		component.WithInitialFocus(),
	)
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
	title := lipgloss.NewStyle().Bold(true).Foreground(s.th.Accent).Render("sextant pending")
	status := s.renderStatus(width)
	return strings.Join([]string{title, "", content, "", status}, "\n")
}

func (s *Standalone) renderStatus(width int) string {
	st := lipgloss.NewStyle().Foreground(s.th.ForegroundMuted)
	left := fmt.Sprintf("pending=%d", s.inner.Count())
	right := "[j/k] nav  [⏎] answer  [/] filter  [q] quit"
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return st.Render(left + strings.Repeat(" ", gap) + right)
}

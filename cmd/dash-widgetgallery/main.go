// Command dash-widgetgallery is a preview binary for the dash widget toolkit. It
// renders the cursor list, stream viewport, and detail pane side by side in all
// three focus states (idle · selected · active), so a human can eyeball the look
// and a VHS tape can drive the real TTY. It is a dev affordance, not part of the
// dash itself.
//
// Run: go run ./cmd/dash-widgetgallery [--theme light|dark|auto]
//
// Keys: ←/→ (or h/l) cycle which column is selected · ↑/↓ (or k/j) move within
// the active column · enter steps the selected column from selected→active ·
// esc steps back out · t toggles light/dark · q (or ctrl+c) quits.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

func main() {
	themeFlag := flag.String("theme", "auto", "theme: light, dark, or auto")
	flag.Parse()

	m := newModel(resolveTheme(*themeFlag))
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "widget gallery error:", err)
		os.Exit(1)
	}
}

func resolveTheme(name string) theme.Theme {
	switch name {
	case "light":
		return theme.Light()
	case "dark":
		return theme.Dark()
	default:
		return theme.Auto()
	}
}

// model holds the three widgets and which column the layout has selected /
// stepped into. The gallery demonstrates the two-level focus model: the selected
// column is accent-bordered, and Enter steps it to active so its cursor lights
// up.
type model struct {
	th     theme.Theme
	keys   theme.Keymap
	list   widget.List
	stream widget.Stream
	detail widget.Detail

	// selected is the column the layout points at (0 list, 1 stream, 2 detail).
	selected int
	// active is true once the operator has stepped into the selected column.
	active bool
	w, h   int
}

func newModel(th theme.Theme) model {
	keys := theme.DefaultKeymap()

	list := widget.NewList(
		keys,
		clientItem(th, "lena", theme.RoleHuman, theme.StatusConnected),
		clientItem(th, "coordinator-1", theme.RoleCoordinator, theme.StatusConnected),
		clientItem(th, "dispatcher-1", theme.RoleDispatcher, theme.StatusIdle),
		clientItem(th, "agent-alpha", theme.RoleAgent, theme.StatusConnected),
		clientItem(th, "agent-beta", theme.RoleAgent, theme.StatusDraining),
		clientItem(th, "bus", theme.RoleSystem, theme.StatusConnected),
	)

	stream := widget.NewStream(keys)
	stream.SetLines(streamLines())

	detail := widget.NewDetail(keys)
	detail.SetText(detailText())

	return model{th: th, keys: keys, list: list, stream: stream, detail: detail}
}

func clientItem(th theme.Theme, name, role string, st theme.Status) widget.ListItem {
	glyph := lipgloss.NewStyle().Foreground(th.StatusHue(st)).Render(theme.StatusGlyph(st))
	return widget.ListItem{
		Title: name,
		Glyph: glyph,
		Hue:   th.RoleHue(role),
	}
}

func streamLines() []string {
	return []string{
		"lena            chat            let's get the dash building",
		"coordinator-1   spawn.request   agent-alpha: theme toolkit",
		"agent-alpha     spawn.ack       accepted, starting",
		"agent-alpha     workflow.event  step 1/3 palette resolved",
		"agent-alpha     workflow.event  step 2/3 widgets compiling",
		"agent-alpha     artifact.update theme.go +210",
		"agent-beta      drain           going offline for redeploy",
		"coordinator-1   chat            nice — eyeball the gallery",
		"lena            chat            on it",
		"agent-alpha     workflow.event  step 3/3 goldens green",
	}
}

func detailText() string {
	return "Selected: agent-alpha\n\n" +
		"role: agent\nstatus: connected\nkind: workflow.event\n\n" +
		"This detail pane word-wraps a block of text to its width and scrolls " +
		"vertically when the content runs past the visible height. The overflow " +
		"cues at the top and bottom edges show when there is more to read above " +
		"or below the fold. Step in with enter to scroll; step out with esc."
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
	case tea.KeyMsg:
		switch {
		case keyB(m.keys.Quit, msg) || keyB(m.keys.ForceQuit, msg):
			return m, tea.Quit
		case msg.String() == "t":
			if m.th.Variant == theme.VariantLight {
				m.th = theme.Dark()
			} else {
				m.th = theme.Light()
			}
			m.rebuild()
			return m, nil
		case keyB(m.keys.Back, msg):
			m.active = false
			return m, nil
		case keyB(m.keys.Enter, msg):
			m.active = true
			return m, nil
		}

		if !m.active {
			// Layout level: move the selection between columns.
			switch {
			case keyB(m.keys.Left, msg):
				m.selected = (m.selected + 2) % 3
			case keyB(m.keys.Right, msg):
				m.selected = (m.selected + 1) % 3
			}
			return m, nil
		}

		// Active level: route keys into the stepped-in widget.
		switch m.selected {
		case 0:
			m.list, _ = m.list.Update(msg)
		case 1:
			m.stream, _ = m.stream.Update(msg)
		case 2:
			m.detail, _ = m.detail.Update(msg)
		}
	}
	return m, nil
}

// focusFor returns the focus state for a column given the current selection and
// whether the operator has stepped in.
func (m model) focusFor(col int) widget.Focus {
	if col != m.selected {
		return widget.FocusIdle
	}
	if m.active {
		return widget.FocusActive
	}
	return widget.FocusSelected
}

func (m model) View() string {
	if m.w == 0 {
		return "starting widget gallery…"
	}
	colW := m.w / 3
	col3W := m.w - 2*colW
	h := m.h - 1

	listV := widget.Box(m.th, m.focusFor(0), "presence", m.th.RoleHue(theme.RoleHuman),
		m.list.View(m.th, m.focusFor(0)), colW, h)
	streamV := widget.Box(m.th, m.focusFor(1), "stream", m.th.KindHue(theme.KindWorkflowEvent),
		m.stream.View(m.th, m.focusFor(1)), colW, h)
	detailV := widget.Box(m.th, m.focusFor(2), "detail", m.th.KindHue(theme.KindArtifactUpdate),
		m.detail.View(m.th, m.focusFor(2)), col3W, h)

	row := lipgloss.JoinHorizontal(lipgloss.Top, listV, streamV, detailV)
	return lipgloss.JoinVertical(lipgloss.Left, row, m.statusBar())
}

func (m model) statusBar() string {
	cols := []string{"presence", "stream", "detail"}
	state := "selected"
	if m.active {
		state = "active"
	}
	left := lipgloss.NewStyle().Foreground(m.th.Fg).Render(
		fmt.Sprintf(" %s · %s ", cols[m.selected], state),
	)
	hint := lipgloss.NewStyle().Foreground(m.th.Dim).Render(
		"←/→ select · enter step in · esc out · ↑/↓ move · t theme · q quit ",
	)
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hint
	return lipgloss.NewStyle().Background(m.th.Panel).Width(m.w).MaxWidth(m.w).Render(bar)
}

// layout sizes each widget's inner content area to its box's interior.
func (m *model) layout() {
	if m.w == 0 {
		return
	}
	colW := m.w / 3
	col3W := m.w - 2*colW
	h := m.h - 1
	innerH := h - 2
	innerW := colW - 4   // box draws 2 border cols + 2 padding cols
	inner3W := col3W - 4 // detail column may be a touch wider
	m.list.SetSize(innerW, innerH)
	m.stream.SetSize(innerW, innerH)
	m.detail.SetSize(inner3W, innerH)
}

// rebuild reconstructs the widgets against the current theme (used by the theme
// toggle, since list glyph colours are baked at construction).
func (m *model) rebuild() {
	listCursor := m.list.Cursor()
	nm := newModel(m.th)
	nm.selected, nm.active, nm.w, nm.h = m.selected, m.active, m.w, m.h
	for i := 0; i < listCursor; i++ {
		nm.list.MoveDown()
	}
	nm.layout()
	*m = nm
}

// keyB matches a keymap binding (the load-bearing keys all go through the
// keymap, never a literal).
func keyB(b key.Binding, msg tea.KeyMsg) bool { return key.Matches(msg, b) }

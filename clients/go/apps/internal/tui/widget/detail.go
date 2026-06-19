package widget

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
)

// Detail is a scrollable wrapped-text pane: it word-wraps a block of text to its
// width and scrolls vertically when the wrapped text overruns its height. It is
// the detail-on-demand widget (ADR-0023) — the read side of a selected record,
// rendered as plain wrapped text from a theme.Theme and its Focus.
type Detail struct {
	keys theme.Keymap
	text string
	// wrapped holds text word-wrapped to the current width; recomputed on
	// SetText / SetSize.
	wrapped []string
	offset  int

	width, height int
}

// NewDetail builds an empty Detail pane.
func NewDetail(keys theme.Keymap) Detail {
	return Detail{keys: keys}
}

// SetText replaces the pane's text and re-wraps it, resetting scroll to the top.
func (d *Detail) SetText(text string) {
	d.text = text
	d.offset = 0
	d.rewrap()
}

// SetSize sets the inner content area (inside any box chrome) and re-wraps to
// the new width.
func (d *Detail) SetSize(w, h int) {
	d.width, d.height = w, h
	d.rewrap()
	d.clampOffset()
}

// rewrap word-wraps the text to the current width using lipgloss, which is
// rune-width aware.
func (d *Detail) rewrap() {
	if d.width <= 0 || d.text == "" {
		d.wrapped = nil
		return
	}
	rendered := lipgloss.NewStyle().Width(d.width).Render(d.text)
	d.wrapped = strings.Split(rendered, "\n")
}

// Init implements tea.Model. The detail pane has no startup command.
func (d Detail) Init() tea.Cmd { return nil }

// Update scrolls on the keymap's Up/Down bindings; a no-op otherwise. Route keys
// here only when the pane is active.
func (d Detail) Update(msg tea.Msg) (Detail, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, d.keys.Up):
			d.ScrollUp()
		case keyMatches(km, d.keys.Down):
			d.ScrollDown()
		}
	}
	return d, nil
}

// ScrollUp moves the view up one wrapped line.
func (d *Detail) ScrollUp() {
	if d.offset > 0 {
		d.offset--
	}
}

// ScrollDown moves the view down one wrapped line, clamped so the last line
// stays in view.
func (d *Detail) ScrollDown() {
	if d.offset < d.maxOffset() {
		d.offset++
	}
}

func (d Detail) maxOffset() int {
	return maxViewportOffset(len(d.wrapped), d.height)
}

func (d *Detail) clampOffset() {
	if d.offset > d.maxOffset() {
		d.offset = d.maxOffset()
	}
	if d.offset < 0 {
		d.offset = 0
	}
}

// View renders the visible wrapped lines for the given focus, with ↑/↓ overflow
// cues when the text runs past the viewport (brighter when active). Empty text
// renders a dim placeholder.
func (d Detail) View(t theme.Theme, focus Focus) string {
	w := d.width
	if w <= 0 {
		w = 1
	}
	if len(d.wrapped) == 0 {
		return lipgloss.NewStyle().Foreground(t.Dim).Width(w).Render("(nothing selected)")
	}
	return renderViewport(t, focus, d.wrapped, d.offset, d.width, d.height)
}

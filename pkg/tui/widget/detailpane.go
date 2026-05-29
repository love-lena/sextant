package widget

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/theme"
)

// Row is one label/value line in a DetailPane. Value may already be
// styled by the surface (e.g. a colored lifecycle word).
type Row struct {
	Label string
	Value string
}

// Section is a titled group of rows.
type Section struct {
	Title string
	Rows  []Row
}

// DetailPane renders titled label/value sections, scrolled via an
// embedded (top-anchored) StreamViewport. Pointer receiver.
type DetailPane struct {
	sv       *StreamViewport
	sections []Section

	titleStyle lipgloss.Style
	labelStyle lipgloss.Style
}

// NewDetail constructs a DetailPane bound to th's role tokens.
func NewDetail(th theme.Theme) *DetailPane {
	sv := NewStreamViewport(0)
	sv.SetFollow(false) // anchor at the top; a detail view isn't a tail
	return &DetailPane{
		sv:         sv,
		titleStyle: lipgloss.NewStyle().Bold(true).Foreground(th.Accent),
		labelStyle: lipgloss.NewStyle().Foreground(th.ForegroundMuted),
	}
}

// SetSections replaces the content.
func (d *DetailPane) SetSections(secs []Section) {
	d.sections = secs
	d.render()
}

// SetSize informs the pane of its content rect.
func (d *DetailPane) SetSize(w, h int) {
	d.sv.SetSize(w, h)
	d.render()
}

// Update forwards scroll messages to the embedded viewport.
func (d *DetailPane) Update(msg tea.Msg) tea.Cmd { return d.sv.Update(msg) }

// View renders the visible window.
func (d *DetailPane) View() string { return d.sv.View() }

func (d *DetailPane) render() {
	labelW := 0
	for _, s := range d.sections {
		for _, r := range s.Rows {
			if len(r.Label) > labelW {
				labelW = len(r.Label)
			}
		}
	}
	var lines []string
	for i, s := range d.sections {
		if i > 0 {
			lines = append(lines, "")
		}
		if s.Title != "" {
			lines = append(lines, d.titleStyle.Render(s.Title))
		}
		for _, r := range s.Rows {
			label := d.labelStyle.Render(fmt.Sprintf("%-*s", labelW, r.Label))
			lines = append(lines, "  "+label+"  "+r.Value)
		}
	}
	d.sv.SetContent(lines)
}

package widget

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
)

// ListItem is one row in a List: unstyled content plus the hues the widget
// paints it in. The widget owns all styling — callers supply plain text and
// colours, never pre-rendered ANSI — so the cursor bar can repaint a row
// cleanly (no embedded reset codes to splice through, the bug Box's doc warns
// against).
type ListItem struct {
	// Title is the row's text, unstyled.
	Title string
	// Glyph is an optional leading marker (e.g. a status shape), unstyled and
	// drawn before the title. Empty for none.
	Glyph string
	// Hue tints the title. The zero Color inherits the theme foreground.
	Hue lipgloss.Color
	// GlyphHue tints the glyph. The zero Color falls back to Hue (then the theme
	// foreground), so a status glyph can carry its own colour distinct from the
	// title.
	GlyphHue lipgloss.Color
}

// List is a cursor-driven selectable list: a vertical column of rows with a
// movable cursor. It is a Bubble Tea component (Update + View) and renders only
// from a theme.Theme and its Focus. The cursor row is always visible: a
// prominent filled accent bar when active (the focused pane), and a muted dim
// bar when selected or idle — so an unfocused pane still shows its place
// (ADR-0026: panes hold their place; the cue is just muted).
type List struct {
	keys  theme.Keymap
	items []ListItem
	// cursor is the index of the highlighted row.
	cursor int
	// offset is the index of the first visible row (for scrolling past height).
	offset int
	// width and height are the inner content area (excluding any surrounding
	// box).
	width, height int
}

// NewList builds a List with the given keymap and items. Pass theme.DefaultKeymap()
// for the stock bindings, or a merged keymap for overrides.
func NewList(keys theme.Keymap, items ...ListItem) List {
	return List{keys: keys, items: items}
}

// SetSize sets the inner content area the list renders into (the area inside any
// box chrome). It clamps the visible window so the cursor stays in view.
func (l *List) SetSize(w, h int) {
	l.width, l.height = w, h
	l.clampOffset()
}

// SetItems replaces the list's rows, clamping the cursor into range.
func (l *List) SetItems(items []ListItem) {
	l.items = items
	if l.cursor >= len(items) {
		l.cursor = max(0, len(items)-1)
	}
	l.clampOffset()
}

// Items returns the current rows.
func (l List) Items() []ListItem { return l.items }

// Cursor returns the index of the highlighted row, or -1 when the list is empty.
func (l List) Cursor() int {
	if len(l.items) == 0 {
		return -1
	}
	return l.cursor
}

// Selected returns the highlighted item and true, or the zero item and false
// when the list is empty.
func (l List) Selected() (ListItem, bool) {
	if len(l.items) == 0 {
		return ListItem{}, false
	}
	return l.items[l.cursor], true
}

// Init implements tea.Model. The list has no startup command.
func (l List) Init() tea.Cmd { return nil }

// Update moves the cursor on the keymap's Up/Down bindings. It is a no-op for
// other messages; the caller routes keys here only when the list is active.
func (l List) Update(msg tea.Msg) (List, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, l.keys.Up):
			l.MoveUp()
		case keyMatches(km, l.keys.Down):
			l.MoveDown()
		}
	}
	return l, nil
}

// SetCursor moves the cursor to a row index, clamped into range, and scrolls so
// it stays in view. Surfaces use it to drive selection from the outside (e.g.
// mapping a record id to its row); an out-of-range index is clamped to the
// nearest valid row. An empty list ignores it.
func (l *List) SetCursor(i int) {
	if len(l.items) == 0 {
		l.cursor = 0
		return
	}
	if i < 0 {
		i = 0
	}
	if i > len(l.items)-1 {
		i = len(l.items) - 1
	}
	l.cursor = i
	l.clampOffset()
}

// MoveUp moves the cursor up one row, clamped at the top, and scrolls if needed.
func (l *List) MoveUp() {
	if l.cursor > 0 {
		l.cursor--
		l.clampOffset()
	}
}

// MoveDown moves the cursor down one row, clamped at the bottom, and scrolls if
// needed.
func (l *List) MoveDown() {
	if l.cursor < len(l.items)-1 {
		l.cursor++
		l.clampOffset()
	}
}

// clampOffset slides the visible window so the cursor is always within it.
func (l *List) clampOffset() {
	if l.height <= 0 {
		l.offset = 0
		return
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+l.height {
		l.offset = l.cursor - l.height + 1
	}
	if l.offset < 0 {
		l.offset = 0
	}
}

// View renders the visible rows for the given focus. The cursor row is always
// visible: a prominent filled accent bar when active, a dim filled bar when
// selected or idle — so the operator can see what Enter will open before
// stepping in. An empty list renders a dim placeholder.
func (l List) View(t theme.Theme, focus Focus) string {
	w := l.width
	if w <= 0 {
		w = 1
	}
	if len(l.items) == 0 {
		return lipgloss.NewStyle().
			Foreground(t.Dim).
			Width(w).
			Render("(empty)")
	}

	end := l.offset + l.height
	if l.height <= 0 || end > len(l.items) {
		end = len(l.items)
	}

	var b strings.Builder
	for i := l.offset; i < end; i++ {
		it := l.items[i]
		var line string
		if i == l.cursor {
			line = l.renderCursorRow(t, it, w, focus)
		} else {
			line = l.renderRow(t, it, w)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderRow paints a resting row: the glyph and title in their own hues,
// per-segment, clamped to the row width so a long title is truncated (Box must
// never have to re-wrap a list row).
func (l List) renderRow(t theme.Theme, it ListItem, w int) string {
	titleHue := it.Hue
	if titleHue == "" {
		titleHue = t.Fg
	}
	var row strings.Builder
	if it.Glyph != "" {
		glyphHue := it.GlyphHue
		if glyphHue == "" {
			glyphHue = titleHue
		}
		row.WriteString(lipgloss.NewStyle().Foreground(glyphHue).Render(it.Glyph))
		row.WriteByte(' ')
	}
	row.WriteString(lipgloss.NewStyle().Foreground(titleHue).Render(it.Title))
	// Width pads short rows; MaxHeight(1) clamps a long row to one line
	// (truncate, never wrap) so Box gets exactly one row per item.
	return lipgloss.NewStyle().Width(w).MaxWidth(w).MaxHeight(1).Render(row.String())
}

// renderCursorRow paints the cursor row as a filled bar. When active the bar
// uses the accent hue (prominent); when selected or idle it uses the dim hue
// (muted) so the operator sees what Enter will open before stepping in. In
// both cases the row is built from UNSTYLED content so the single background
// style applies cleanly — no inner reset codes to splice through.
func (l List) renderCursorRow(t theme.Theme, it ListItem, w int, focus Focus) string {
	var plain strings.Builder
	if it.Glyph != "" {
		plain.WriteString(it.Glyph)
		plain.WriteByte(' ')
	}
	plain.WriteString(it.Title)
	bg := t.Dim
	fg := t.Bg
	bold := false
	if focus == FocusActive {
		bg = t.Accent
		fg = t.OnAccent
		bold = true
	}
	return lipgloss.NewStyle().
		Background(bg).
		Foreground(fg).
		Bold(bold).
		Width(w).
		MaxWidth(w).
		MaxHeight(1).
		Render(plain.String())
}

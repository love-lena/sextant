package widget

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// ListItem is one row in a List. Title is the row text; Hue, when set, tints the
// leading glyph and the title (e.g. a role hue) — leave it the zero Color to
// inherit the theme foreground. Glyph is an optional leading marker (e.g. a
// status shape) rendered before the title.
type ListItem struct {
	// Title is the row's text.
	Title string
	// Glyph is an optional leading marker (e.g. a status shape) drawn before the
	// title. Empty for none.
	Glyph string
	// Hue tints the glyph and title. The zero Color inherits the theme
	// foreground.
	Hue lipgloss.Color
}

// List is a cursor-driven selectable list: a vertical column of rows with a
// movable cursor. It is a Bubble Tea component (Update + View) and renders only
// from a theme.Theme and its Focus. The cursor row is highlighted only when the
// list is active (stepped in); a selected-but-not-active list shows an accent
// border with no cursor bar, the middle of the three focus states.
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

// View renders the visible rows for the given focus. The cursor row is
// highlighted with a filled bar only when focus is FocusActive — that is the
// active-vs-selected distinction inside the widget. An empty list renders a
// dim placeholder.
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
		hue := it.Hue
		if hue == "" {
			hue = t.Fg
		}

		var row strings.Builder
		if it.Glyph != "" {
			row.WriteString(it.Glyph)
			row.WriteByte(' ')
		}
		row.WriteString(it.Title)

		line := lipgloss.NewStyle().Foreground(hue).Render(row.String())

		if i == l.cursor && focus == FocusActive {
			// Active cursor: a filled accent bar across the full width.
			line = lipgloss.NewStyle().
				Background(t.Accent).
				Foreground(t.OnAccent).
				Bold(true).
				Width(w).
				MaxWidth(w).
				Render(row.String())
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

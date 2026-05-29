package widget

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/theme"
)

// ListActionKind enumerates what a ListPane.Update produced.
type ListActionKind int

const (
	// ListNone is navigation / a no-op — nothing for the surface to do.
	ListNone ListActionKind = iota
	// ListSelected means the operator pressed enter on a row.
	ListSelected
)

// ListAction is returned by ListPane.Update so the embedding surface can
// react to selection without the widget addressing other panes itself.
type ListAction[T any] struct {
	Kind   ListActionKind
	Row    T
	HasRow bool
}

// ListConfig configures a ListPane. Render is required; Filter==nil
// disables the `/` filter; KeyID is optional (stable row identity).
type ListConfig[T any] struct {
	Header string // column header line (already laid out by the surface)
	Render func(row T, selected bool) string
	Empty  string // empty-state text
	Filter func(row T, query string) bool
	KeyID  func(row T) string
}

type listKeys struct {
	Up, Down, Top, Bottom, Select, Filter key.Binding
}

func defaultListKeys() listKeys {
	return listKeys{
		Up:     key.NewBinding(key.WithKeys("k", "up")),
		Down:   key.NewBinding(key.WithKeys("j", "down")),
		Top:    key.NewBinding(key.WithKeys("g", "home")),
		Bottom: key.NewBinding(key.WithKeys("G", "end")),
		Select: key.NewBinding(key.WithKeys("enter")),
		Filter: key.NewBinding(key.WithKeys("/")),
	}
}

// ListPane is a generic cursor list. Pointer receiver, mutates in place
// (see package doc). The surface owns per-row styling via Config.Render;
// the pane owns navigation, the scroll window, the header, the empty
// state, and (opt-in) the `/` filter.
type ListPane[T any] struct {
	cfg  ListConfig[T]
	keys listKeys

	rows []T
	view []int // indices into rows visible after filtering

	cursor int // index into view
	top    int // first visible view-index (scroll window)
	w, h   int

	filtering bool
	query     string

	headerStyle lipgloss.Style
	emptyStyle  lipgloss.Style
	filterStyle lipgloss.Style
}

// NewList constructs a ListPane bound to th's role tokens.
func NewList[T any](cfg ListConfig[T], th theme.Theme) *ListPane[T] {
	return &ListPane[T]{
		cfg:         cfg,
		keys:        defaultListKeys(),
		headerStyle: lipgloss.NewStyle().Bold(true).Foreground(th.ForegroundMuted),
		emptyStyle:  lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		filterStyle: lipgloss.NewStyle().Foreground(th.Accent),
	}
}

// SetRows replaces the backing rows and recomputes the filtered view.
func (l *ListPane[T]) SetRows(rows []T) {
	l.rows = rows
	l.refilter()
}

// SetSize informs the pane of its content rect.
func (l *ListPane[T]) SetSize(w, h int) {
	l.w, l.h = w, h
	l.clampWindow()
}

// Selected returns the row under the cursor (ok=false when the view is
// empty).
func (l *ListPane[T]) Selected() (T, bool) {
	var zero T
	if l.cursor < 0 || l.cursor >= len(l.view) {
		return zero, false
	}
	return l.rows[l.view[l.cursor]], true
}

// Len returns the number of visible (post-filter) rows.
func (l *ListPane[T]) Len() int { return len(l.view) }

// Filtering reports whether the `/` filter input is active.
func (l *ListPane[T]) Filtering() bool { return l.filtering }

// Update handles a message and returns an action. Pointer receiver.
func (l *ListPane[T]) Update(msg tea.Msg) ListAction[T] {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return ListAction[T]{Kind: ListNone}
	}
	if l.filtering {
		l.updateFilter(km)
		return ListAction[T]{Kind: ListNone}
	}
	switch {
	case key.Matches(km, l.keys.Down):
		l.move(1)
	case key.Matches(km, l.keys.Up):
		l.move(-1)
	case key.Matches(km, l.keys.Top):
		l.cursor = 0
		l.clampWindow()
	case key.Matches(km, l.keys.Bottom):
		l.cursor = maxInt(0, len(l.view)-1)
		l.clampWindow()
	case l.cfg.Filter != nil && key.Matches(km, l.keys.Filter):
		l.filtering = true
		l.query = ""
	case key.Matches(km, l.keys.Select):
		if row, ok := l.Selected(); ok {
			return ListAction[T]{Kind: ListSelected, Row: row, HasRow: true}
		}
	}
	return ListAction[T]{Kind: ListNone}
}

func (l *ListPane[T]) updateFilter(km tea.KeyMsg) {
	switch km.Type {
	case tea.KeyEsc:
		l.filtering = false
		l.query = ""
		l.refilter()
	case tea.KeyEnter:
		l.filtering = false
		l.refilter()
	case tea.KeyBackspace:
		if len(l.query) > 0 {
			l.query = l.query[:len(l.query)-1]
			l.refilter()
		}
	case tea.KeyRunes:
		l.query += string(km.Runes)
		l.refilter()
	}
}

func (l *ListPane[T]) refilter() {
	l.view = l.view[:0]
	for i, r := range l.rows {
		if l.cfg.Filter == nil || l.query == "" || l.cfg.Filter(r, l.query) {
			l.view = append(l.view, i)
		}
	}
	if l.cursor >= len(l.view) {
		l.cursor = maxInt(0, len(l.view)-1)
	}
	l.clampWindow()
}

func (l *ListPane[T]) move(delta int) {
	l.cursor += delta
	if l.cursor < 0 {
		l.cursor = 0
	}
	if l.cursor >= len(l.view) {
		l.cursor = maxInt(0, len(l.view)-1)
	}
	l.clampWindow()
}

// clampWindow keeps the cursor inside the visible scroll window.
func (l *ListPane[T]) clampWindow() {
	rows := l.rowsBudget()
	if rows <= 0 {
		l.top = 0
		return
	}
	if l.cursor < l.top {
		l.top = l.cursor
	}
	if l.cursor >= l.top+rows {
		l.top = l.cursor - rows + 1
	}
	if l.top < 0 {
		l.top = 0
	}
}

// rowsBudget is how many data rows fit (height minus header + filter line).
func (l *ListPane[T]) rowsBudget() int {
	b := l.h
	if l.cfg.Header != "" {
		b--
	}
	if l.filtering {
		b--
	}
	return b
}

// View renders the pane content (no surrounding chrome — that's the
// host's job).
func (l *ListPane[T]) View() string {
	var b strings.Builder
	if l.filtering {
		b.WriteString(l.filterStyle.Render("/" + l.query))
		b.WriteByte('\n')
	}
	if l.cfg.Header != "" {
		b.WriteString(l.headerStyle.Render(l.cfg.Header))
		b.WriteByte('\n')
	}
	if len(l.view) == 0 {
		b.WriteString(l.emptyStyle.Render(l.cfg.Empty))
		return b.String()
	}
	rows := l.rowsBudget()
	end := l.top + rows
	if rows <= 0 || end > len(l.view) {
		end = len(l.view)
	}
	for i := l.top; i < end; i++ {
		row := l.rows[l.view[i]]
		b.WriteString(l.cfg.Render(row, i == l.cursor))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

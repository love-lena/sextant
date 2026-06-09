package surface

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// Browser is the master-detail pane of ADR-0024: a list at rest that opens a
// row's detail in the same pane. It is the shared machinery the three concrete
// browsers (clients, topics, artifacts) embed — each supplies only its data
// (by calling setRows as rows arrive) and an openRow hook that builds the detail
// Surface for a selected row. Browser owns the list ⇄ detail state, the focus,
// and the open/pop keys.
//
// Navigation is pane state (ADR-0024/0026):
//   - At the list, Up/Down move the cursor; Enter opens the selected row's
//     detail in place; Back (Esc) does nothing — there is no level above the
//     list, and leaving the pane is the host's focus move, not a key here.
//   - In the detail, every key but Back goes to the inner detail Surface; Back
//     pops back to the list (consumed here, so the detail never sees it). Each
//     Back pops exactly one level. An open detail is pane state: it persists
//     until the operator pops it, regardless of where focus goes.
//
// A concrete browser embeds Browser, overrides Init (to load its rows) and
// Update (to fold in its own data messages, then delegate to Browser.Update),
// and overrides Stop when it owns a resource beyond the open detail.
type Browser struct {
	id   string
	base string // the list-mode title (e.g. "Topics")
	keys theme.Keymap
	th   theme.Theme

	list  widget.List
	focus widget.Focus
	w, h  int

	// openRow builds the detail Surface for the row at the given cursor index and
	// the title to show while it is open (e.g. "Topic · plan"). A nil Surface
	// means the row cannot be opened (Enter is a no-op).
	openRow func(cursor int) (Surface, string)

	detail      Surface // the open detail, or nil at the list level
	detailTitle string
}

// newBrowser builds a Browser with an empty list. The concrete browser fills the
// list via setRows and supplies openRow to turn a selected row into a detail.
func newBrowser(id, base string, keys theme.Keymap, th theme.Theme, openRow func(cursor int) (Surface, string)) Browser {
	return Browser{
		id:      id,
		base:    base,
		keys:    keys,
		th:      th,
		list:    widget.NewList(keys),
		focus:   widget.FocusIdle,
		openRow: openRow,
	}
}

// ID is the stable pane id (e.g. "clients").
func (b *Browser) ID() string { return b.id }

// Title is the base name at the list level and the detail's title while one is
// open (e.g. "Topics" → "Topic · plan").
func (b *Browser) Title() string {
	if b.detail != nil && b.detailTitle != "" {
		return b.detailTitle
	}
	return b.base
}

// SetSize stores the inner area and sizes whichever of the list or the open
// detail is showing (both, so a pop-back is already sized).
func (b *Browser) SetSize(w, h int) {
	b.w, b.h = w, h
	b.list.SetSize(w, h)
	if b.detail != nil {
		b.detail.SetSize(w, h)
	}
}

// relayoutList resizes the list within the recorded inner area, reserving the
// bottom row for an error footer when hasErr is true — so a full list never
// clips the footer. A concrete browser calls this from SetSize (passing its
// current error state) and from any Update path that changes the error state, so
// the reserved row is always in step with visibility.
func (b *Browser) relayoutList(hasErr bool) {
	listH := b.h
	if hasErr {
		listH--
	}
	if listH < 1 {
		listH = 1
	}
	b.list.SetSize(b.w, listH)
}

// SetFocus sets the three-state focus; the open detail tracks it so its in-body
// cue (cursor, compose line) matches.
func (b *Browser) SetFocus(f widget.Focus) {
	b.focus = f
	if b.detail != nil {
		b.detail.SetFocus(f)
	}
}

// CapturingText delegates to the open detail (a conversation's compose, a
// review's comment line); the list itself never captures text.
func (b *Browser) CapturingText() bool {
	if b.detail != nil {
		return b.detail.CapturingText()
	}
	return false
}

// SetTheme re-themes the list (taken at View time) and the open detail.
func (b *Browser) SetTheme(t theme.Theme) {
	b.th = t
	if b.detail != nil {
		b.detail.SetTheme(t)
	}
}

// Init defaults to no work; a concrete browser overrides it to load its rows.
func (b *Browser) Init() tea.Cmd { return nil }

// Update is the list ⇄ detail machinery. A concrete browser handles its own data
// messages first, then delegates here for navigation and detail delegation.
func (b *Browser) Update(msg tea.Msg) tea.Cmd {
	if b.detail != nil {
		// In the detail: Back pops to the list (consumed here); everything else —
		// keys, the feed pump, compose — goes to the inner Surface.
		if km, ok := msg.(tea.KeyMsg); ok && key.Matches(km, b.keys.Back) {
			b.popToList()
			return nil
		}
		return b.detail.Update(msg)
	}
	// At the list: Enter opens the selected row. Back is a no-op here — the list
	// is the pane's top level (ADR-0026: Esc at the list does nothing; leaving
	// the pane is a focus move, not a level).
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(km, b.keys.Enter):
			return b.openSelected()
		case key.Matches(km, b.keys.Back):
			return nil
		}
	}
	var cmd tea.Cmd
	b.list, cmd = b.list.Update(msg)
	return cmd
}

// View renders the open detail, or the list at rest.
func (b *Browser) View() string {
	if b.detail != nil {
		return b.detail.View()
	}
	return b.list.View(b.th, b.focus)
}

// Stop tears down the open detail (if any). A concrete browser that owns another
// resource (e.g. the topics discovery feed) overrides Stop to release it too,
// after calling stopDetail.
func (b *Browser) Stop() { b.stopDetail() }

// setRows replaces the list rows, preserving the cursor position (clamped). The
// concrete browser calls it whenever its data arrives or refreshes.
func (b *Browser) setRows(items []widget.ListItem) {
	cur := b.list.Cursor()
	b.list.SetItems(items)
	b.list.SetCursor(cur)
}

// openSelected builds and opens the detail for the selected row, sizing and
// focusing it and returning its Init command (e.g. to start a feed). It is a
// no-op when the list is empty or the row cannot be opened.
func (b *Browser) openSelected() tea.Cmd {
	if _, ok := b.list.Selected(); !ok {
		return nil
	}
	sub, title := b.openRow(b.list.Cursor())
	if sub == nil {
		return nil
	}
	b.detail, b.detailTitle = sub, title
	b.detail.SetSize(b.w, b.h)
	b.detail.SetFocus(b.focus)
	return b.detail.Init()
}

// popToList closes the open detail and returns to the list. The detail's Stop
// releases its resources (a busfeed subscription, an artifact watch); re-opening
// builds a fresh one.
func (b *Browser) popToList() { b.stopDetail() }

// stopDetail stops and clears the open detail, if any.
func (b *Browser) stopDetail() {
	if b.detail != nil {
		b.detail.Stop()
		b.detail = nil
		b.detailTitle = ""
	}
}

// inDetail reports whether a detail is open (the concrete browsers and tests use
// it to decide whether a data refresh should also touch the list).
func (b *Browser) inDetail() bool { return b.detail != nil }

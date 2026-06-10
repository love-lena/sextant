package surface

import (
	"context"
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// fakeDetail is a minimal detail Surface that records teardown, so the browser
// state-machine tests can assert the open/pop/Stop lifecycle without a real feed.
type fakeDetail struct {
	id        string
	stopped   bool
	capturing bool
}

func (f *fakeDetail) ID() string             { return f.id }
func (f *fakeDetail) Title() string          { return f.id }
func (f *fakeDetail) SetSize(int, int)       {}
func (f *fakeDetail) SetFocus(widget.Focus)  {}
func (f *fakeDetail) CapturingText() bool    { return f.capturing }
func (f *fakeDetail) SetTheme(theme.Theme)   {}
func (f *fakeDetail) Init() tea.Cmd          { return nil }
func (f *fakeDetail) Update(tea.Msg) tea.Cmd { return nil }
func (f *fakeDetail) View() string           { return "DETAIL:" + f.id }
func (f *fakeDetail) Stop()                  { f.stopped = true }

var _ Surface = (*fakeDetail)(nil)

// Browser is a complete Surface on its own (a static list browser), so the tests
// drive it directly with a fake openRow.

func keyMsg(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func newTestBrowser(open func(cursor int) (Surface, string)) *Browser {
	b := newBrowser("fake", "Fakes", theme.DefaultKeymap(), theme.Dark(), open)
	b.setRows([]widget.ListItem{{Title: "row0"}, {Title: "row1"}, {Title: "row2"}}, nil)
	b.SetSize(40, 10)
	b.SetFocus(widget.FocusActive)
	return &b
}

func TestBrowserEnterOpensDetailEscPops(t *testing.T) {
	det := &fakeDetail{id: "d"}
	b := newTestBrowser(func(cursor int) (Surface, string) {
		return det, "Fake · " + strconv.Itoa(cursor)
	})

	// At rest: list mode.
	if b.inDetail() {
		t.Fatal("new browser should start at the list, not in a detail")
	}
	if got := b.Title(); got != "Fakes" {
		t.Fatalf("list-mode Title = %q, want %q", got, "Fakes")
	}

	// Down then Enter opens the detail for row 1.
	b.Update(keyMsg(tea.KeyDown))
	b.Update(keyMsg(tea.KeyEnter))
	if !b.inDetail() {
		t.Fatal("Enter should open the detail")
	}
	if got := b.Title(); got != "Fake · 1" {
		t.Fatalf("detail Title = %q, want %q (cursor followed Down)", got, "Fake · 1")
	}
	if got := b.View(); got != "DETAIL:d" {
		t.Fatalf("detail View = %q, want the inner detail's view", got)
	}

	// Esc in the detail pops back to the list and tears the detail down.
	if cmd := b.Update(keyMsg(tea.KeyEsc)); cmd != nil {
		t.Fatal("Esc in detail should be consumed (no command escapes to the layout)")
	}
	if b.inDetail() {
		t.Fatal("Esc should pop the detail back to the list")
	}
	if !det.stopped {
		t.Fatal("popping the detail should Stop it (release its resources)")
	}
	if got := b.Title(); got != "Fakes" {
		t.Fatalf("after pop, Title = %q, want %q", got, "Fakes")
	}
}

// TestBrowserEscAtListIsNoOp pins ADR-0026: the list is the pane's top level,
// so Esc there does nothing — no command escapes, no state changes. Leaving the
// pane is the host's focus move, never a key the browser acts on.
func TestBrowserEscAtListIsNoOp(t *testing.T) {
	b := newTestBrowser(func(int) (Surface, string) { return &fakeDetail{}, "x" })
	if cmd := b.Update(keyMsg(tea.KeyEsc)); cmd != nil {
		t.Fatalf("Esc at the list should be a no-op, got command %#v", cmd())
	}
	if b.inDetail() {
		t.Fatal("Esc at the list must not change the browser state")
	}
	if got := b.Title(); got != "Fakes" {
		t.Fatalf("Esc at the list changed the title: %q", got)
	}
}

// TestBrowserPastedTextNeverMatchesBindings pins the burst/paste guard at the
// browser level: a multi-rune KeyRunes chunk can spell a binding name ("esc",
// "enter"), but it is content — it must not open a row at the list nor pop an
// open detail; in a detail it flows through to the inner surface (a compose).
func TestBrowserPastedTextNeverMatchesBindings(t *testing.T) {
	chunk := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	det := &fakeDetail{id: "d"}
	b := newTestBrowser(func(int) (Surface, string) { return det, "t" })

	b.Update(chunk("enter"))
	if b.inDetail() {
		t.Fatal("pasted \"enter\" must not open a detail")
	}
	b.Update(keyMsg(tea.KeyEnter))
	if !b.inDetail() {
		t.Fatal("precondition: detail open")
	}
	b.Update(chunk("esc"))
	if !b.inDetail() {
		t.Fatal("pasted \"esc\" must not pop the open detail")
	}
}

// TestBrowserCapturingTextDelegatesToDetail: the list never captures text; with
// a detail open, CapturingText is the detail's answer (a conversation's live
// compose makes the whole pane capturing, so the host's q types instead of
// quitting).
func TestBrowserCapturingTextDelegatesToDetail(t *testing.T) {
	det := &fakeDetail{id: "d", capturing: true}
	b := newTestBrowser(func(int) (Surface, string) { return det, "t" })
	if b.CapturingText() {
		t.Fatal("a list must not capture text")
	}
	b.Update(keyMsg(tea.KeyEnter))
	if !b.CapturingText() {
		t.Fatal("an open capturing detail must make the browser capturing")
	}
	det.capturing = false
	if b.CapturingText() {
		t.Fatal("the browser must track the detail's live capturing state")
	}
}

func TestBrowserEnterNoOpWhenRowUnopenable(t *testing.T) {
	b := newTestBrowser(func(int) (Surface, string) { return nil, "" }) // openRow declines
	if cmd := b.Update(keyMsg(tea.KeyEnter)); cmd != nil {
		t.Fatal("Enter on an unopenable row should be a no-op")
	}
	if b.inDetail() {
		t.Fatal("an unopenable row must not enter a detail")
	}
}

func TestBrowserStopTearsDownOpenDetail(t *testing.T) {
	det := &fakeDetail{id: "d"}
	b := newTestBrowser(func(int) (Surface, string) { return det, "t" })
	b.Update(keyMsg(tea.KeyEnter))
	if !b.inDetail() {
		t.Fatal("precondition: detail open")
	}
	b.Stop()
	if !det.stopped {
		t.Fatal("Stop must tear down the open detail")
	}
}

// TestSetRowsPreservesSelectionByKey pins the refresh-shift fix: rows re-sort
// and insert live (clients/artifacts re-snapshot every 2s, topics grow on
// discovery), so a cursor preserved by INDEX would put a different item under
// it between seeing and pressing Enter. The selection follows the stable row
// key; only when the key disappears does it fall back to the clamped index.
func TestSetRowsPreservesSelectionByKey(t *testing.T) {
	rows := func(names ...string) []widget.ListItem {
		items := make([]widget.ListItem, len(names))
		for i, n := range names {
			items[i] = widget.ListItem{Title: n}
		}
		return items
	}
	b := newBrowser("fake", "Fakes", theme.DefaultKeymap(), theme.Dark(), nil)
	b.SetSize(40, 10)

	b.setRows(rows("alpha", "beta", "gamma"), []string{"alpha", "beta", "gamma"})
	b.list.SetCursor(1) // select beta

	// A refresh inserts a row that sorts above the selection.
	b.setRows(rows("aardvark", "alpha", "beta", "gamma"), []string{"aardvark", "alpha", "beta", "gamma"})
	if got := b.list.Cursor(); got != 2 {
		t.Fatalf("selection slid off beta: cursor %d, want 2", got)
	}

	// The selected item disappears: fall back to the clamped index.
	b.setRows(rows("alpha", "gamma"), []string{"alpha", "gamma"})
	if got := b.list.Cursor(); got != 1 {
		t.Fatalf("vanished key should clamp the old index: cursor %d, want 1", got)
	}
}

// TestClientsBrowserSelectionSurvivesRefresh drives the same guarantee through
// a concrete browser end to end: select a client, let a refresh snapshot insert
// one that sorts above it, and Enter must open the DM the operator was looking
// at — not whichever client slid under the cursor.
func TestClientsBrowserSelectionSurvivesRefresh(t *testing.T) {
	c := NewClientsBrowser(context.Background(), nil, theme.Dark(), theme.DefaultKeymap())
	c.SetSize(40, 10)
	c.SetFocus(widget.FocusActive)
	c.Update(ClientsLoadedMsg{Clients: []sextant.ClientInfo{
		{ID: "01B", DisplayName: "beta"},
		{ID: "01G", DisplayName: "gamma"},
	}})
	c.Update(keyMsg(tea.KeyDown)) // select gamma

	// The 2s re-snapshot lands with a new client sorting above the selection.
	c.Update(ClientsLoadedMsg{Clients: []sextant.ClientInfo{
		{ID: "01A", DisplayName: "alpha"},
		{ID: "01B", DisplayName: "beta"},
		{ID: "01G", DisplayName: "gamma"},
	}})

	c.Update(keyMsg(tea.KeyEnter))
	if got := c.Title(); got != "DM · gamma" {
		t.Fatalf("Enter opened %q, want \"DM · gamma\" (the item selected before the refresh)", got)
	}
}

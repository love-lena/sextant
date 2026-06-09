package surface

import (
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	b.setRows([]widget.ListItem{{Title: "row0"}, {Title: "row1"}, {Title: "row2"}})
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

package traces

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

func loaded(t *testing.T) *Model {
	t.Helper()
	m := New(Options{})
	m.SetSize(80, 20)
	spans := []sextantproto.TraceSpan{
		{SpanID: "root", SpanName: "root", Timestamp: time.Unix(0, 0)},
		{SpanID: "child", ParentSpanID: "root", SpanName: "child", Timestamp: time.Unix(1, 0)},
	}
	next, _ := m.Update(spansLoadedMsg{spans: spans})
	return next.(*Model)
}

func TestEnterTogglesCollapse(t *testing.T) {
	m := loaded(t)
	if m.VisibleRows() != 2 {
		t.Fatalf("rows = %d, want 2 (expanded)", m.VisibleRows())
	}
	// cursor starts on root; Enter collapses it → child hidden.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*Model)
	if m.VisibleRows() != 1 {
		t.Fatalf("rows after collapse = %d, want 1", m.VisibleRows())
	}
	// Enter again re-expands.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*Model)
	if m.VisibleRows() != 2 {
		t.Fatalf("rows after re-expand = %d, want 2", m.VisibleRows())
	}
}

func TestEscCollapsesFocused(t *testing.T) {
	m := loaded(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(*Model)
	if m.VisibleRows() != 1 {
		t.Fatalf("rows after esc-collapse = %d, want 1", m.VisibleRows())
	}
}

func TestQuitEmitsDone(t *testing.T) {
	m := New(Options{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q produced no cmd")
	}
	if _, ok := cmd().(component.DoneMsg); !ok {
		t.Fatalf("q did not emit DoneMsg, got %T", cmd())
	}
}

func TestLoadError(t *testing.T) {
	m := New(Options{})
	m.SetSize(80, 20)
	next, _ := m.Update(spansLoadedMsg{err: errFake})
	m = next.(*Model)
	if got := m.View(); got == "" {
		t.Fatal("error view should render something")
	}
}

var errFake = errTest("boom")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "traces-show" {
			found = true
			if meta.New() == nil {
				t.Fatal("traces-show factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("traces-show not registered")
	}
}

var _ component.Component = (*Model)(nil)

package logsview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/tui/component"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

func TestAppendsLines(t *testing.T) {
	m := New(Options{})
	m.SetSize(80, 10)
	next, _ := m.Update(lineMsg{ev: widget.Event[string]{Item: "line one"}, ok: true})
	m = next.(*Model)
	next, _ = m.Update(lineMsg{ev: widget.Event[string]{Item: "line two"}, ok: true})
	m = next.(*Model)
	if m.LineCount() != 2 {
		t.Fatalf("line count = %d, want 2", m.LineCount())
	}
	if !strings.Contains(m.View(), "line two") {
		t.Fatalf("view missing appended line: %q", m.View())
	}
}

func TestErrorSurfacesBanner(t *testing.T) {
	m := New(Options{})
	m.SetSize(80, 10)
	next, _ := m.Update(lineMsg{ev: widget.Event[string]{Err: errTest("tail broke")}, ok: true})
	m = next.(*Model)
	if !strings.Contains(m.View(), "tail broke") {
		t.Fatalf("error not surfaced: %q", m.View())
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

func TestClosedStreamStopsCleanly(t *testing.T) {
	m := New(Options{})
	m.SetSize(80, 10)
	_, cmd := m.Update(lineMsg{ok: false})
	if cmd != nil {
		t.Fatal("closed stream should not schedule another read")
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "daemon-logs" {
			found = true
			if meta.New() == nil {
				t.Fatal("daemon-logs factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("daemon-logs not registered")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

var _ component.Component = (*Model)(nil)

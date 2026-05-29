package contextview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sessionlog"
	"github.com/love-lena/sextant/pkg/tui/component"
)

func feed(m *Model, ev sessionlog.Event) *Model {
	next, _ := m.Update(eventMsg{ev: ev, ok: true})
	return next.(*Model)
}

func TestModeKeySwitchesRender(t *testing.T) {
	m := New(Options{})
	m.SetSize(80, 20)
	m = feed(m, sessionlog.AssistantMessage{
		ContentBlocks: []sessionlog.Block{sessionlog.TextBlock{Text: "hi there"}},
	})
	// Default raw mode: the assistant record has no RawLine here, so raw
	// shows nothing; switch to conversation (key "2").
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = next.(*Model)
	if m.Mode() != sessionlog.ModeConversation {
		t.Fatalf("mode = %v, want conversation", m.Mode())
	}
	if !strings.Contains(m.renderedBuffer(), "assistant: hi there") {
		t.Fatalf("conversation render missing text: %q", m.renderedBuffer())
	}
	// Switch to tools (key "3"): the text block produces nothing.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = next.(*Model)
	if strings.Contains(m.renderedBuffer(), "hi there") {
		t.Fatalf("tools mode should not show assistant text: %q", m.renderedBuffer())
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
	m.SetSize(80, 20)
	next, cmd := m.Update(eventMsg{ok: false})
	m = next.(*Model)
	if cmd != nil {
		t.Fatal("closed stream should not schedule another read")
	}
}

func TestDefaultModeIsRaw(t *testing.T) {
	m := New(Options{})
	if m.Mode() != sessionlog.ModeRaw {
		t.Fatalf("default mode = %v, want raw", m.Mode())
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "agents-context" {
			found = true
			if meta.New() == nil {
				t.Fatal("agents-context factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("agents-context not registered")
	}
}

var _ component.Component = (*Model)(nil)

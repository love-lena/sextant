package pending

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/tui/component"
)

func newModel(t *testing.T) *Model {
	t.Helper()
	m := New(Options{})
	m.SetSize(80, 12)
	return m
}

func upsert(m *Model, r Request) *Model {
	next, _ := m.Update(requestUpsertMsg{req: r})
	return next.(*Model)
}

func TestDerivesUnansweredAndSorts(t *testing.T) {
	m := newModel(t)
	r1 := Request{RequestID: uuid.New(), Question: "first"}
	r2 := Request{RequestID: uuid.New(), Question: "second"}
	m = upsert(m, r1)
	m = upsert(m, r2)
	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2", m.Count())
	}
	next, _ := m.Update(responseMsg{requestID: r1.RequestID})
	m = next.(*Model)
	if m.Count() != 1 {
		t.Fatalf("count after answer = %d, want 1", m.Count())
	}
	sel, ok := m.Selected()
	if !ok || sel.RequestID != r2.RequestID {
		t.Fatalf("remaining = %v, want r2", sel)
	}
}

func TestQuitEmitsDone(t *testing.T) {
	m := newModel(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q produced no cmd")
	}
	if _, ok := cmd().(component.DoneMsg); !ok {
		t.Fatalf("q did not emit DoneMsg, got %T", cmd())
	}
}

func TestEnterEmitsOpenMsg(t *testing.T) {
	m := newModel(t)
	rid := uuid.New()
	m = upsert(m, Request{RequestID: rid, FromUUID: uuid.New(), Question: "q?"})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	open, ok := cmd().(component.OpenMsg)
	if !ok {
		t.Fatalf("enter did not emit OpenMsg, got %T", cmd())
	}
	if open.Target != "pending-answer" || open.ID != rid.String() {
		t.Fatalf("OpenMsg = %+v, want {pending-answer, %s}", open, rid)
	}
}

func TestViewShowsRequestAndEmptyState(t *testing.T) {
	m := newModel(t)
	if !strings.Contains(m.View(), "no pending requests") {
		t.Fatalf("empty state missing: %q", m.View())
	}
	m = upsert(m, Request{RequestID: uuid.New(), FromUUID: uuid.New(), Question: "deploy now?", Urgency: "high"})
	v := m.View()
	if !strings.Contains(v, "deploy now?") || !strings.Contains(v, "high") {
		t.Fatalf("view missing request: %q", v)
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "pending-list" {
			found = true
			if meta.New() == nil {
				t.Fatal("pending-list factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("pending-list not registered")
	}
}

// Compile-time assertion that *Model satisfies the Component contract.
var _ component.Component = (*Model)(nil)

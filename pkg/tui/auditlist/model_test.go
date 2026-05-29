package auditlist

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

func loaded(t *testing.T) *Model {
	t.Helper()
	m := New(Options{})
	m.SetSize(110, 12)
	next, _ := m.Update(rowsLoadedMsg{rows: []sextantproto.QueryAuditRow{
		{ID: uuid.New(), Ts: time.Unix(1000, 0), Actor: "operator", Action: "spawn_agent", Result: "ok"},
		{ID: uuid.New(), Ts: time.Unix(2000, 0), Actor: "operator", Action: "kill_agent", Result: "denied"},
	}})
	return next.(*Model)
}

func TestLoadsAndRenders(t *testing.T) {
	m := loaded(t)
	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2", m.Count())
	}
	v := m.View()
	if !strings.Contains(v, "spawn_agent") || !strings.Contains(v, "denied") {
		t.Fatalf("view missing audit rows: %q", v)
	}
}

func TestEnterEmitsOpenDetail(t *testing.T) {
	m := loaded(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	open, ok := cmd().(component.OpenMsg)
	if !ok {
		t.Fatalf("enter did not emit OpenMsg, got %T", cmd())
	}
	if open.Target != "audit-detail" {
		t.Fatalf("OpenMsg target = %q, want audit-detail", open.Target)
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
	m.SetSize(80, 10)
	next, _ := m.Update(rowsLoadedMsg{err: errTest("clickhouse down")})
	m = next.(*Model)
	if !strings.Contains(m.View(), "clickhouse down") {
		t.Fatalf("error not surfaced: %q", m.View())
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "audit-list" {
			found = true
			if meta.New() == nil {
				t.Fatal("audit-list factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("audit-list not registered")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

var _ component.Component = (*Model)(nil)

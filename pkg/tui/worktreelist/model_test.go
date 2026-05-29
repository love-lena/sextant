package worktreelist

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

func loaded(t *testing.T) *Model {
	t.Helper()
	m := New(Options{})
	m.SetSize(100, 12)
	next, _ := m.Update(worktreesLoadedMsg{worktrees: []sextantproto.WorktreeInfo{
		{Name: "feat-x", Branch: "feat-x", BaseBranch: "main", Status: sextantproto.WorktreeStatusActive, LastActivity: time.Unix(1000, 0)},
		{Name: "feat-y", Branch: "feat-y", BaseBranch: "main", Status: sextantproto.WorktreeStatusMerged, LastActivity: time.Unix(2000, 0)},
	}})
	return next.(*Model)
}

func TestLoadsAndRenders(t *testing.T) {
	m := loaded(t)
	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2", m.Count())
	}
	v := m.View()
	if !strings.Contains(v, "feat-x") || !strings.Contains(v, "active") {
		t.Fatalf("view missing worktree: %q", v)
	}
}

func TestEnterEmitsOpenDiff(t *testing.T) {
	m := loaded(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	open, ok := cmd().(component.OpenMsg)
	if !ok {
		t.Fatalf("enter did not emit OpenMsg, got %T", cmd())
	}
	if open.Target != "worktree-diff" || open.ID != "feat-x" {
		t.Fatalf("OpenMsg = %+v, want {worktree-diff, feat-x}", open)
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
	next, _ := m.Update(worktreesLoadedMsg{err: errTest("daemon down")})
	m = next.(*Model)
	if !strings.Contains(m.View(), "daemon down") {
		t.Fatalf("error not surfaced: %q", m.View())
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "worktree-list" {
			found = true
			if meta.New() == nil {
				t.Fatal("worktree-list factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("worktree-list not registered")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

var _ component.Component = (*Model)(nil)

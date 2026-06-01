package agentdetail

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

func loaded(t *testing.T, msg detailLoadedMsg) *Model {
	t.Helper()
	m := New(Options{})
	m.SetSize(80, 24)
	next, _ := m.Update(msg)
	return next.(*Model)
}

func TestRendersAgentSection(t *testing.T) {
	id := uuid.New()
	m := loaded(t, detailLoadedMsg{
		status: sextantproto.AgentStatus{
			UUID: id, Name: "assistant", Lifecycle: "running", Version: 3,
			UpdatedAt: time.Unix(1000, 0),
		},
		template: "claude-seed",
	})
	v := m.View()
	for _, want := range []string{"agent", "assistant", "running", "claude-seed", "no session yet"} {
		if !strings.Contains(v, want) {
			t.Fatalf("detail missing %q in:\n%s", want, v)
		}
	}
}

func TestRendersSessionAndWorktreeWhenPresent(t *testing.T) {
	id := uuid.New()
	m := loaded(t, detailLoadedMsg{
		status: sextantproto.AgentStatus{
			UUID: id, Name: "a", Lifecycle: "running",
			SessionLog: &sextantproto.SessionLogInfo{
				SessionID:          "s-123",
				ContainerJSONLPath: "/home/agent/.claude/projects/-workspace/s-123.jsonl",
				SnapshotPath:       "/data/agents/snap.jsonl",
			},
		},
		worktree: &sextantproto.WorktreeInfo{Branch: "feat-x", BaseBranch: "main", Status: sextantproto.WorktreeStatusActive, Path: "/wt/feat-x"},
	})
	v := m.View()
	for _, want := range []string{"s-123", "s-123.jsonl", "/data/agents/snap.jsonl", "feat-x ⦿ main", "/wt/feat-x"} {
		if !strings.Contains(v, want) {
			t.Fatalf("detail missing %q in:\n%s", want, v)
		}
	}
}

func TestGracefulDegradeMissingTemplate(t *testing.T) {
	m := loaded(t, detailLoadedMsg{
		status: sextantproto.AgentStatus{UUID: uuid.New(), Name: "a", Lifecycle: "running"},
		// no template, no worktree
	})
	if !strings.Contains(m.View(), "—") {
		t.Fatalf("missing-field dash not rendered:\n%s", m.View())
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
	next, _ := m.Update(detailLoadedMsg{err: errTest("agent not found")})
	m = next.(*Model)
	if !strings.Contains(m.View(), "agent not found") {
		t.Fatalf("error not surfaced: %q", m.View())
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	found := false
	for _, meta := range component.List() {
		if meta.Name == "agent-detail" {
			found = true
			if meta.New() == nil {
				t.Fatal("agent-detail factory returned nil")
			}
		}
	}
	if !found {
		t.Fatal("agent-detail not registered")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

var _ component.Component = (*Model)(nil)

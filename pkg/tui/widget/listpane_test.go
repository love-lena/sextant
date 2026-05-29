package widget

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/theme"
)

type lrow struct{ name string }

func testList() *ListPane[lrow] {
	l := NewList(ListConfig[lrow]{
		Header: "NAME",
		Render: func(r lrow, sel bool) string {
			if sel {
				return "> " + r.name
			}
			return "  " + r.name
		},
		Empty: "(none)",
		KeyID: func(r lrow) string { return r.name },
		Filter: func(r lrow, q string) bool {
			return strings.Contains(r.name, q)
		},
	}, theme.DefaultTheme())
	l.SetSize(40, 10)
	return l
}

func kp(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestListEmptyState(t *testing.T) {
	l := testList()
	if !strings.Contains(l.View(), "(none)") {
		t.Fatalf("empty state missing: %q", l.View())
	}
}

func TestListNavClampsAndSelects(t *testing.T) {
	l := testList()
	l.SetRows([]lrow{{"alpha"}, {"bravo"}, {"charlie"}})
	if r, _ := l.Selected(); r.name != "alpha" {
		t.Fatalf("start cursor = %s, want alpha", r.name)
	}
	l.Update(kp("j"))
	l.Update(kp("j"))
	if r, _ := l.Selected(); r.name != "charlie" {
		t.Fatalf("after jj = %s, want charlie", r.name)
	}
	l.Update(kp("j")) // clamp at bottom
	if r, _ := l.Selected(); r.name != "charlie" {
		t.Fatalf("clamp = %s, want charlie", r.name)
	}
	l.Update(kp("g"))
	if r, _ := l.Selected(); r.name != "alpha" {
		t.Fatalf("g top = %s, want alpha", r.name)
	}
	l.Update(kp("G"))
	if r, _ := l.Selected(); r.name != "charlie" {
		t.Fatalf("G bottom = %s, want charlie", r.name)
	}
	act := l.Update(kp("enter"))
	if act.Kind != ListSelected || !act.HasRow || act.Row.name != "charlie" {
		t.Fatalf("enter action = %+v, want Selected charlie", act)
	}
}

func TestListRendersHeaderAndCursor(t *testing.T) {
	l := testList()
	l.SetRows([]lrow{{"alpha"}, {"bravo"}})
	v := l.View()
	if !strings.Contains(v, "NAME") {
		t.Fatalf("header missing: %q", v)
	}
	if !strings.Contains(v, "> alpha") {
		t.Fatalf("cursor row not marked: %q", v)
	}
	if !strings.Contains(v, "  bravo") {
		t.Fatalf("non-cursor row wrong: %q", v)
	}
}

func TestListFilter(t *testing.T) {
	l := testList()
	l.SetRows([]lrow{{"alpha"}, {"bravo"}, {"charlie"}})
	l.Update(kp("/")) // enter filter mode
	l.Update(kp("r")) // matches bravo + charlie
	if l.Len() != 2 {
		t.Fatalf("filter 'r' matched %d, want 2", l.Len())
	}
	l.Update(kp("a")) // "ra" — matches only bravo
	if l.Len() != 1 {
		t.Fatalf("filter 'ra' matched %d, want 1 (bravo)", l.Len())
	}
	l.Update(kp("backspace")) // back to "r"
	if l.Len() != 2 {
		t.Fatalf("after backspace matched %d, want 2", l.Len())
	}
	l.Update(kp("esc")) // clear filter
	if l.Len() != 3 {
		t.Fatalf("after esc matched %d, want 3 (filter cleared)", l.Len())
	}
}

func TestListWindowScrollsToKeepCursorVisible(t *testing.T) {
	l := NewList(ListConfig[lrow]{
		Header: "NAME",
		Render: func(r lrow, sel bool) string { return r.name },
		Empty:  "(none)",
	}, theme.DefaultTheme())
	l.SetSize(40, 4) // header(1) → 3 data rows fit
	rows := make([]lrow, 10)
	for i := range rows {
		rows[i] = lrow{name: string(rune('a' + i))}
	}
	l.SetRows(rows)
	for i := 0; i < 9; i++ {
		l.Update(kp("j"))
	}
	v := l.View()
	if !strings.Contains(v, "j") { // cursor row (10th) must be visible
		t.Fatalf("bottom cursor row not in window: %q", v)
	}
	if strings.Contains(v, "a") { // top rows should have scrolled off
		t.Fatalf("window did not scroll; top row still visible: %q", v)
	}
}

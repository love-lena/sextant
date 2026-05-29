package widget

import (
	"fmt"
	"strings"
	"testing"
)

func TestStreamViewportFollowsAndCapsRing(t *testing.T) {
	s := NewStreamViewport(3) // ring buffer keeps last 3 lines
	s.SetSize(20, 2)
	s.Append("l1", "l2", "l3", "l4", "l5")
	if s.LineCount() != 3 {
		t.Fatalf("ring buffer kept %d lines, want 3", s.LineCount())
	}
	v := s.View()
	if !strings.Contains(v, "l5") {
		t.Fatalf("following should pin to bottom (l5): %q", v)
	}
	if strings.Contains(v, "l1") || strings.Contains(v, "l2") {
		t.Fatalf("dropped lines still present: %q", v)
	}
}

func TestStreamViewportGotoTopStopsFollowing(t *testing.T) {
	s := NewStreamViewport(100)
	s.SetSize(20, 2)
	for i := 1; i <= 10; i++ {
		s.Append(fmt.Sprintf("line%02d", i))
	}
	if !s.Following() {
		t.Fatal("should start following at bottom")
	}
	s.Update(kp("g")) // goto top
	if s.Following() {
		t.Fatal("g (top) should stop following")
	}
	if !strings.Contains(s.View(), "line01") {
		t.Fatalf("after g, top should show line01: %q", s.View())
	}
	s.Append("line11") // must NOT autoscroll while not following
	if !strings.Contains(s.View(), "line01") {
		t.Fatalf("append while paused should hold position: %q", s.View())
	}
	s.Update(kp("G")) // resume follow
	if !s.Following() {
		t.Fatal("G (bottom) should resume following")
	}
	if !strings.Contains(s.View(), "line11") {
		t.Fatalf("after G, bottom should show line11: %q", s.View())
	}
}

func TestStreamViewportSetContentReplaces(t *testing.T) {
	s := NewStreamViewport(100)
	s.SetSize(20, 5)
	s.Append("old1", "old2")
	s.SetContent([]string{"new1", "new2"})
	v := s.View()
	if strings.Contains(v, "old1") {
		t.Fatalf("SetContent should replace, not append: %q", v)
	}
	if !strings.Contains(v, "new2") {
		t.Fatalf("SetContent missing new content: %q", v)
	}
}

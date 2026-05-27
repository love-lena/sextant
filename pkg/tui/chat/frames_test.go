package chat

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func TestFrameMsgAppendsTurn(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"})
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	msg := frameMsg{Frame: Frame{
		Ts:        t0,
		FrameKind: sextantproto.FrameAssistantText,
		Body:      map[string]any{"text": "hi from agent"},
	}}
	next, _ := m.Update(msg)
	mm := next.(Model)
	if got := len(mm.Turns()); got != 1 {
		t.Fatalf("turn count: want 1, got %d", got)
	}
	if mm.Turns()[0].Text != "hi from agent" {
		t.Errorf("text: %q", mm.Turns()[0].Text)
	}
}

func TestFrameMsgHoldsSelectionWhenScrolledUp(t *testing.T) {
	t.Parallel()
	// Operator is on turn 1 of 4 (scrolled up). A new frame arrives.
	// Selection must stay where the operator parked it (spec §"Open
	// Q5": hold position).
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m.focus = FocusStream // explicit: FocusStream + sel < last means "scrolled up, hold position"
	m.selection = 1
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	msg := frameMsg{Frame: Frame{Ts: t0, FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": "later"}}}
	next, _ := m.Update(msg)
	mm := next.(Model)
	if mm.Selection() != 1 {
		t.Errorf("selection moved: want 1, got %d", mm.Selection())
	}
	if got := len(mm.Turns()); got != len(seedTurns())+1 {
		t.Errorf("turn count: want %d, got %d", len(seedTurns())+1, got)
	}
}

func TestFrameMsgAdvancesSelectionWhenAtBottom(t *testing.T) {
	t.Parallel()
	// Operator is on the last turn (auto-tail). New frame arrives —
	// selection follows so auto-tail keeps tracking.
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	prev := m.Selection()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	msg := frameMsg{Frame: Frame{Ts: t0, FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": "next"}}}
	next, _ := m.Update(msg)
	mm := next.(Model)
	if mm.Selection() != prev+1 {
		t.Errorf("auto-tail: want %d, got %d", prev+1, mm.Selection())
	}
}

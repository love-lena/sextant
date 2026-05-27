package chat

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func envAt(t time.Time, kind sextantproto.FrameKind, body map[string]any, toolName string) Frame {
	return Frame{
		Ts:        t,
		FrameKind: kind,
		ToolName:  toolName,
		Body:      body,
	}
}

func TestFramesToTurns_GroupsToolCallsUnderPriorAssistantTurn(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "calling read_file"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{"path": "main.go"}, "read_file"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolResult, map[string]any{"bytes": float64(120)}, "read_file"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "done"}, ""),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Actor != ActorAgent || turns[0].Text != "calling read_file" {
		t.Errorf("turn0: %+v", turns[0])
	}
	if len(turns[0].ToolCalls) != 1 {
		t.Fatalf("turn0 tool calls: want 1, got %d", len(turns[0].ToolCalls))
	}
	tc := turns[0].ToolCalls[0]
	if tc.Name != "read_file" || tc.Status != ToolStatusOK {
		t.Errorf("tool call: %+v", tc)
	}
	if turns[1].Actor != ActorAgent || turns[1].Text != "done" {
		t.Errorf("turn1: %+v", turns[1])
	}
}

func TestFramesToTurns_UserPromptStartsNewTurn(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		{Ts: t0, Actor: ActorUser, Text: "hi"},
		envAt(t0.Add(time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "hello"}, ""),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Actor != ActorUser || turns[0].Text != "hi" {
		t.Errorf("turn0: %+v", turns[0])
	}
	if turns[1].Actor != ActorAgent {
		t.Errorf("turn1 actor: %v", turns[1].Actor)
	}
}

func TestFramesToTurns_ToolCallFailureMarksStatusFailed(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "trying"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{}, "bad_tool"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolResult, map[string]any{"error": "boom"}, "bad_tool"),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 || len(turns[0].ToolCalls) != 1 {
		t.Fatalf("turns=%+v", turns)
	}
	if got := turns[0].ToolCalls[0].Status; got != ToolStatusFailed {
		t.Errorf("status: want failed, got %v", got)
	}
}

func TestFramesToTurns_PopulatesArgAndDuration(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "looking"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{"path": "main.go"}, "read_file"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameToolResult, map[string]any{"bytes": float64(120)}, "read_file"),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 || len(turns[0].ToolCalls) != 1 {
		t.Fatalf("turns=%+v", turns)
	}
	tc := turns[0].ToolCalls[0]
	if tc.Arg != "main.go" {
		t.Errorf("Arg: want main.go, got %q", tc.Arg)
	}
	if tc.Duration != 2*time.Second {
		t.Errorf("Duration: want 2s, got %v", tc.Duration)
	}
}

func TestFramesToTurns_ToolCallWithoutPriorAgentTurnSynthesizesOne(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameToolCall, map[string]any{"path": "x"}, "read_file"),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 {
		t.Fatalf("expected 1 synthetic turn, got %d: %+v", len(turns), turns)
	}
	if turns[0].Actor != ActorAgent {
		t.Errorf("synthetic turn actor: want ActorAgent, got %v", turns[0].Actor)
	}
	if len(turns[0].ToolCalls) != 1 || turns[0].ToolCalls[0].Name != "read_file" {
		t.Errorf("synthetic turn missing tool call: %+v", turns[0])
	}
}

func TestFramesToTurns_OrphanedToolResultIsIgnored(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "hello"}, ""),
		// Tool result with no matching call — should be silently dropped.
		envAt(t0.Add(time.Second), sextantproto.FrameToolResult, map[string]any{"bytes": float64(0)}, "ghost"),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if len(turns[0].ToolCalls) != 0 {
		t.Errorf("orphan tool result attached itself: %+v", turns[0])
	}
}

func TestFramesToTurns_EmptySystemNoteIsDropped(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "hello"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameSystemNote, map[string]any{}, ""),
		envAt(t0.Add(2*time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "world"}, ""),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns (system note dropped), got %d", len(turns))
	}
	for _, tn := range turns {
		if tn.Actor == ActorSystem {
			t.Errorf("system turn leaked: %+v", tn)
		}
	}
}

func TestFramesToTurns_SystemNoteWithDataKeysSummarizes(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameSystemNote, map[string]any{"event": "started", "session": "abc"}, ""),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn (summarized), got %d", len(turns))
	}
	if turns[0].Actor != ActorSystem {
		t.Errorf("actor: %v", turns[0].Actor)
	}
	// Keys sort alphabetically: "event=started session=abc"
	if turns[0].Text != "event=started session=abc" {
		t.Errorf("text: %q", turns[0].Text)
	}
}

func TestFramesToTurns_ConcurrentSameNameToolCallsResolveInOrder(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "searching"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{"query": "alpha"}, "search"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolCall, map[string]any{"query": "beta"}, "search"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameToolResult, map[string]any{"hits": float64(5)}, "search"),
		envAt(t0.Add(4*time.Second), sextantproto.FrameToolResult, map[string]any{"error": "rate limited"}, "search"),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 || len(turns[0].ToolCalls) != 2 {
		t.Fatalf("turns=%+v", turns)
	}
	// First call (Arg=alpha) gets the first result (ok).
	if turns[0].ToolCalls[0].Arg != "alpha" || turns[0].ToolCalls[0].Status != ToolStatusOK {
		t.Errorf("first call: %+v", turns[0].ToolCalls[0])
	}
	// Second call (Arg=beta) gets the second result (failed).
	if turns[0].ToolCalls[1].Arg != "beta" || turns[0].ToolCalls[1].Status != ToolStatusFailed {
		t.Errorf("second call: %+v", turns[0].ToolCalls[1])
	}
}

package sessionlog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	if m, _ := ParseMode(""); m != ModeRaw {
		t.Fatalf("empty = %v, want raw", m)
	}
	if m, err := ParseMode("Conversation"); err != nil || m != ModeConversation {
		t.Fatalf("Conversation = %v,%v", m, err)
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Fatal("bogus should error")
	}
}

func TestRenderConversation(t *testing.T) {
	ev := AssistantMessage{
		ContentBlocks: []Block{
			TextBlock{Text: "hello"},
			ToolUseBlock{ID: "toolu_1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
		},
	}
	out := RenderLine(ev, ModeConversation, nil)
	if !strings.Contains(out, "assistant: hello") {
		t.Fatalf("missing assistant text: %q", out)
	}
	if !strings.Contains(out, "tool_use[toolu_1] Bash") {
		t.Fatalf("missing tool_use: %q", out)
	}
}

func TestRenderTools(t *testing.T) {
	asst := AssistantMessage{ContentBlocks: []Block{
		ToolUseBlock{ID: "t1", Name: "Read", Input: json.RawMessage(`{}`)},
	}}
	if got := RenderLine(asst, ModeTools, nil); !strings.Contains(got, "call t1 Read") {
		t.Fatalf("tools call line wrong: %q", got)
	}
	usr := UserMessage{ContentBlocks: []Block{
		ToolResultBlock{ToolUseID: "t1", Text: "file contents", IsError: false},
	}}
	if got := RenderLine(usr, ModeTools, nil); !strings.Contains(got, "result[ok] t1") {
		t.Fatalf("tools result line wrong: %q", got)
	}
}

func TestRenderThinking(t *testing.T) {
	ev := AssistantMessage{
		CommonFields:  CommonFields{UUID: "u1"},
		ContentBlocks: []Block{ThinkingBlock{Thinking: "let me think"}},
	}
	if got := RenderLine(ev, ModeThinking, nil); !strings.Contains(got, "thinking[u1]: let me think") {
		t.Fatalf("thinking line wrong: %q", got)
	}
	// Non-assistant → empty.
	if got := RenderLine(UserMessage{Text: "hi"}, ModeThinking, nil); got != "" {
		t.Fatalf("thinking on user = %q, want empty", got)
	}
}

func TestRenderUsageAccumulates(t *testing.T) {
	acc := &UsageAccumulator{}
	ev := AssistantMessage{
		Model: "claude", StopReason: "end_turn",
		Usage: Usage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 100},
	}
	out := RenderLine(ev, ModeUsage, acc)
	if !strings.Contains(out, "turn=1") || !strings.Contains(out, "in=10") || !strings.Contains(out, "hit=1.00") {
		t.Fatalf("usage line wrong: %q", out)
	}
	// A record with no usage is skipped.
	if got := RenderLine(AssistantMessage{}, ModeUsage, acc); got != "" {
		t.Fatalf("empty-usage record = %q, want skipped", got)
	}
}

func TestRenderTree(t *testing.T) {
	ev := AssistantMessage{CommonFields: CommonFields{UUID: "u2", ParentUUID: "u1", IsSidechain: true}}
	if got := RenderLine(ev, ModeTree, nil); !strings.Contains(got, "[sidechain] u2 parent=u1") {
		t.Fatalf("tree line wrong: %q", got)
	}
}

func TestRenderRawReturnsVerbatim(t *testing.T) {
	raw := []byte(`{"type":"mode","mode":"normal"}`)
	ev := RawEvent{raw: raw, kind: "mode"}
	if got := RenderLine(ev, ModeRaw, nil); got != string(raw) {
		t.Fatalf("raw = %q, want verbatim", got)
	}
}

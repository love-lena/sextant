package fixtures

import (
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Demo is the canonical fixture wired into every visual capture and
// preview binary. The frame transcript matches the bespoke fixture
// cmd/sextant-tui-chat-preview/main.go shipped pre-migration so visual
// goldens stay stable across the migration.
//
// Deterministic UUIDs make tape diffs trivial — every screenshot
// against the same fixture produces the same on-screen ids.
var (
	demoAliceUUID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	demoBobUUID   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	demoCarolUUID = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	demoPending1  = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	demoPending2  = uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
)

// DemoAliceUUID exposes the canonical "alice" agent UUID for callers
// (preview binaries, tape commands) that need to address a known
// transcript.
func DemoAliceUUID() uuid.UUID { return demoAliceUUID }

func buildDemo() Fixture {
	t0 := time.Date(2026, 5, 25, 14, 12, 8, 0, time.UTC)
	updated := t0.Add(2 * time.Minute)
	agents := []sextantproto.AgentSummary{
		{
			UUID:      demoAliceUUID,
			Name:      "alice",
			Type:      "claude-code",
			Template:  "default",
			Lifecycle: string(sextantproto.LifecycleRunning),
			Version:   3,
			UpdatedAt: updated,
		},
		{
			UUID:      demoBobUUID,
			Name:      "bob",
			Type:      "claude-code",
			Template:  "researcher",
			Lifecycle: string(sextantproto.LifecyclePaused),
			Version:   1,
			UpdatedAt: updated.Add(-15 * time.Minute),
		},
		{
			UUID:      demoCarolUUID,
			Name:      "carol",
			Type:      "claude-code",
			Template:  "default",
			Lifecycle: string(sextantproto.LifecycleDefined),
			Version:   1,
			UpdatedAt: updated.Add(-2 * time.Hour),
		},
	}
	convos := map[uuid.UUID][]Frame{
		demoAliceUUID: aliceTranscript(t0),
	}
	pending := []sextantproto.UserInputRequestPayload{
		{
			RequestID: demoPending1,
			FromUUID:  demoAliceUUID,
			Question:  "should I delete the stale worktree at .claude/worktrees/feat-old?",
			Options:   []string{"yes", "no", "list_first"},
			Urgency:   "normal",
		},
		{
			RequestID: demoPending2,
			FromUUID:  demoBobUUID,
			Question:  "merge research-notes-2026-05 into main now?",
			Urgency:   "low",
		},
	}
	return Fixture{
		Name:          "demo",
		Agents:        agents,
		Conversations: convos,
		Pending:       pending,
		Operator:      "lena",
	}
}

// aliceTranscript reproduces the bespoke fixture that lived inline in
// cmd/sextant-tui-chat-preview/main.go before this package took
// ownership of the canned demo data. Kept frame-for-frame identical so
// tape goldens captured under the old binary still compare cleanly.
func aliceTranscript(t0 time.Time) []Frame {
	return []Frame{
		{Ts: t0, Actor: ActorUser, Text: "look at cmd/sextant/conversation.go and tell me what it does"},
		{
			Ts: t0.Add(2 * time.Second), FrameKind: sextantproto.FrameAssistantText,
			Body: map[string]any{"text": "Let me read that file."},
		},
		{
			Ts: t0.Add(3 * time.Second), FrameKind: sextantproto.FrameToolCall,
			ToolName: "read_file", Body: map[string]any{"path": "cmd/sextant/conversation.go"},
		},
		{
			Ts: t0.Add(4 * time.Second), FrameKind: sextantproto.FrameToolResult,
			ToolName: "read_file", Body: map[string]any{"bytes": float64(8421)},
		},
		{
			Ts: t0.Add(6 * time.Second), FrameKind: sextantproto.FrameAssistantText,
			Body: map[string]any{"text": "It streams agent frames + lifecycle events to stdout. Default mode is a forever-live tail; --tail exits on lifecycle.ended; --from-seq N resumes from a JetStream sequence; --json toggles NDJSON output."},
		},
		{Ts: t0.Add(28 * time.Second), Actor: ActorUser, Text: "what changes if I add --read?"},
		{
			Ts: t0.Add(30 * time.Second), FrameKind: sextantproto.FrameAssistantText,
			Body: map[string]any{"text": "--read isn't implemented yet — it's listed in the spec as the read-only TUI variant. We'd hide the composer and disable INSERT mode."},
		},
		{
			Ts: t0.Add(31 * time.Second), FrameKind: sextantproto.FrameToolCall,
			ToolName: "grep", Body: map[string]any{"query": "--read"},
		},
		{
			Ts: t0.Add(32 * time.Second), FrameKind: sextantproto.FrameToolResult,
			ToolName: "grep", Body: map[string]any{"error": "no matches"},
		},
		{Ts: t0.Add(60 * time.Second), Actor: ActorUser, Text: "ok make it so. start with the renderer, do bubbletea + lipgloss + rounded panes"},
		{
			Ts: t0.Add(63 * time.Second), FrameKind: sextantproto.FrameAssistantText,
			Body: map[string]any{"text": "Drafting view.go now. Going to use lipgloss.RoundedBorder for the stream + composer panes, a single ▌ glyph for the selection mark, and a dark-gray background tint behind the selected turn so the bar carries across wrapped content."},
		},
		{
			Ts: t0.Add(70 * time.Second), FrameKind: sextantproto.FrameToolCall,
			ToolName: "write_file", Body: map[string]any{"path": "pkg/tui/chat/view.go"},
		},
		{
			Ts: t0.Add(74 * time.Second), FrameKind: sextantproto.FrameToolResult,
			ToolName: "write_file", Body: map[string]any{"bytes": float64(6200)},
		},
		{
			Ts: t0.Add(76 * time.Second), FrameKind: sextantproto.FrameAssistantText,
			Body: map[string]any{"text": "Done — view.go now lays out header + boxed stream + boxed composer + status strip. Tool calls hang under their parent agent turn with a └─ connector."},
		},
	}
}

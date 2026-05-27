// sextant-tui-chat-preview is a design-iteration binary for pkg/tui/chat.
// It boots the chat Model in a real bubbletea program with a seeded
// transcript so the renderer can be exercised without spinning up a
// daemon + agent. Not shipped to operators — kept around as a fast
// loop for visual tweaks. Companion to cmd/sextant-tui-agents/ (the
// other in-repo TUI dev binary).
//
// Run with --read for the read-only variant.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/chat"
)

func main() {
	read := flag.Bool("read", false, "preview the read-only variant (no composer)")
	flag.Parse()

	t0 := time.Date(2026, 5, 25, 14, 12, 8, 0, time.UTC)
	frames := []chat.Frame{
		{Ts: t0, Actor: chat.ActorUser, Text: "look at cmd/sextant/conversation.go and tell me what it does"},
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
		{Ts: t0.Add(28 * time.Second), Actor: chat.ActorUser, Text: "what changes if I add --read?"},
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
		{Ts: t0.Add(60 * time.Second), Actor: chat.ActorUser, Text: "ok make it so. start with the renderer, do bubbletea + lipgloss + rounded panes"},
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
	turns := chat.FramesToTurns(frames)

	m := chat.New(chat.Options{AgentName: "alice", Branch: "main", Read: *read}).WithTurns(turns)
	// Wrap with the Standalone host so the preview renders the same
	// chrome (header + status bar) as the production sextant
	// conversation flow. Pre-refactor the chrome lived inside
	// Model.View; post-refactor it lives on Standalone.
	standalone := chat.NewStandalone(m)

	prog := tea.NewProgram(standalone, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "preview: %v\n", err)
		os.Exit(1)
	}
}

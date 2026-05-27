// sextant-tui-chat-preview is a design-iteration binary for pkg/tui/chat.
// It boots the chat Model in a real bubbletea program with the
// pkg/fixtures.Demo transcript so the renderer can be exercised without
// spinning up a daemon + agent. Not shipped to operators — kept around
// as a fast loop for visual tweaks. Companion to cmd/sextant-tui-agents/
// (the other in-repo TUI dev binary).
//
// Run with --read for the read-only variant.
//
// Pre-2026-05-27 this binary carried its own bespoke transcript inline.
// Migrated to pkg/fixtures.Demo per feat-tui-vhs-remaining so the
// preview and the VHS captures share the same dataset (single source
// of truth for "what the chat looks like with content").
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/fixtures"
	"github.com/love-lena/sextant/pkg/tui/chat"
)

func main() {
	read := flag.Bool("read", false, "preview the read-only variant (no composer)")
	flag.Parse()

	demo, ok := fixtures.Get("demo")
	if !ok {
		fmt.Fprintln(os.Stderr, "preview: fixtures.Get(\"demo\") returned ok=false")
		os.Exit(1)
	}
	frames := fixtures.ChatFrames(demo, fixtures.DemoAliceUUID())
	turns := chat.FramesToTurns(frames)

	m := chat.New(chat.Options{AgentName: "alice", Branch: "main", Read: *read}).WithTurns(turns)
	// Wrap with the Standalone host so the preview renders the same
	// chrome (header + status bar) as the production sextant
	// conversation flow.
	standalone := chat.NewStandalone(m)

	prog := tea.NewProgram(standalone, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "preview: %v\n", err)
		os.Exit(1)
	}
}

package chat

import (
	"github.com/love-lena/sextant/pkg/tui/component"
)

// init self-registers the chat component with the global registry from
// pkg/tui/component. The discovery menu (`sextant tui`) and the dash
// launcher both walk component.List() to enumerate Tier 1 surfaces.
//
// The New factory deliberately constructs a Model with empty Options.
// The Component is fully functional with no agent ID (it just shows
// an empty stream and disabled composer) — callers that want to
// target a specific agent should use pkg/tui/chat.New(Options{...})
// directly or fire a LoadMsg into the Component after mount.
//
// Command path `agents chat` follows the resource-verb layout (see
// `conventions/tui-conventions.md` and `cmd/sextant/chat.go`).
func init() {
	component.Register(component.Meta{
		Name:        "chat",
		Description: "Open the chat TUI for an agent",
		Command:     "agents chat",
		New:         func() component.Component { return New(Options{}) },
		Arg:         "agent",
		ArgKind:     "agent",
		NoIFlag:     true, // `agents chat <agent>` is interactive by default; no -i
	})
}

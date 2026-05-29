package agentdetail

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the agent-detail component for `sextant tui` +
// dash discovery. The New factory builds a Model with no Bus / AgentID —
// the host injects them when mounting.
func init() {
	component.Register(component.Meta{
		Name:        "agent-detail",
		Description: "Inspect one agent (lifecycle, template, worktree, session)",
		Command:     "agents show",
		New:         func() component.Component { return New(Options{}) },
		Arg:         "agent",
		ArgKind:     "agent",
	})
}

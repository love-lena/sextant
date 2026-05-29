package contextview

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the agents-context component. The New factory
// builds a Model with no event stream — the host wires one when mounting.
func init() {
	component.Register(component.Meta{
		Name:        "agents-context",
		Description: "Tail an agent's raw SDK session context",
		Command:     "agents context",
		New:         func() component.Component { return New(Options{}) },
		Arg:         "agent",
		ArgKind:     "agent",
	})
}

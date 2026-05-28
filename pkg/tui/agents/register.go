package agents

import (
	"github.com/love-lena/sextant/pkg/tui/component"
)

// init self-registers the agents-list component with the global
// registry from pkg/tui/component. The discovery menu (`sextant tui`)
// and the dash launcher both walk component.List() to enumerate
// Tier 1 surfaces.
//
// The New factory deliberately constructs a Model with no Bus — the
// dash supplies its own bus when mounting the pane, and tests can
// inject a fake. Callers that want a Bus from the start should use
// pkg/tui/agents.New(Options{Bus: ...}) directly rather than the
// registry factory.
func init() {
	component.Register(component.Meta{
		Name:        "agents-list",
		Description: "Browse and manage running agents",
		Command:     "agents list",
		New:         func() component.Component { return New(Options{}) },
	})
}

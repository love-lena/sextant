package pending

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the pending-list component. The New factory builds
// a Model with no Bus — the standalone/dash host injects one when mounting
// (mirrors pkg/tui/agents.register). Discovery (`sextant tui`) and the
// dash walk component.List().
func init() {
	component.Register(component.Meta{
		Name:        "pending-list",
		Description: "Review and answer pending user-input requests",
		Command:     "pending list",
		New:         func() component.Component { return New(Options{}) },
	})
}

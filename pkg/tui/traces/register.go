package traces

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the traces-show component for `sextant tui` + dash
// discovery. The New factory builds a Model with no Bus / TraceID — the
// host injects them when mounting.
func init() {
	component.Register(component.Meta{
		Name:        "traces-show",
		Description: "Explore a distributed trace as a span tree",
		Command:     "traces show",
		New:         func() component.Component { return New(Options{}) },
	})
}

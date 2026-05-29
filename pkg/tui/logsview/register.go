package logsview

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the daemon-logs component for `sextant tui`
// discovery. The New factory builds a Model with no stream — the host
// wires a TailSource when mounting.
func init() {
	component.Register(component.Meta{
		Name:        "daemon-logs",
		Description: "Tail the daemon log file",
		Command:     "daemon logs",
		New:         func() component.Component { return New(Options{}) },
	})
}

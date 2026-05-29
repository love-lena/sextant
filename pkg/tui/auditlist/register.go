package auditlist

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the audit-list component for `sextant tui` + dash
// discovery. The New factory builds a Model with no Bus — the host
// injects one when mounting.
func init() {
	component.Register(component.Meta{
		Name:        "audit-list",
		Description: "Browse the audit log (last 24h)",
		Command:     "audit list",
		New:         func() component.Component { return New(Options{}) },
	})
}

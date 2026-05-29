package worktreelist

import "github.com/love-lena/sextant/pkg/tui/component"

// init self-registers the worktree-list component for `sextant tui` +
// dash discovery. The New factory builds a Model with no Bus — the host
// injects one when mounting.
func init() {
	component.Register(component.Meta{
		Name:        "worktree-list",
		Description: "Browse worktrees (diff/merge/delete)",
		Command:     "worktree list",
		New:         func() component.Component { return New(Options{}) },
	})
}

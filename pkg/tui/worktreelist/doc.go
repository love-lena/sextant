// Package worktreelist is the Tier 1 component for `sextant worktree
// list -i`: a ListPane over the worktree_list RPC. Enter emits an open
// intent (diff) for the focused worktree; r refreshes. Per the RFC
// (plans/rfc-tui-workstream.md, P2). Merge/delete row actions are
// deferred (destructive — need the confirm flow) and emit intents only.
package worktreelist

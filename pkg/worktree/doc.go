// Package worktree manages git worktrees as agent workspaces. Each
// agent's container mounts its worktree at /workspace; multiple
// agents work on independent branches concurrently.
//
// The package wraps:
//
//   - git worktree commands (add, remove, list)
//   - the NATS KV `worktrees` bucket (the registry)
//   - the NATS KV `locks.merge` key (merge serialization)
//
// Merges land via a dedicated transient merge worktree so the
// operator's main checkout is never mutated. See
// specs/architecture.md §11 "Merge strategy".
//
// Plan: plans/bootstrap.md#M14
// Spec: specs/architecture.md §11, conventions/git-workflow.md.
package worktree

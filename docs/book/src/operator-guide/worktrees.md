# Worktrees

Sextant's parallel-iteration pattern: each agent gets its own git worktree mounted as `/workspace`. Agents work on independent branches concurrently; merges into `main` are serialized by a NATS KV lock.

For the manager-side details, see [worktree](../components/worktree.md). This chapter is operator-facing.

## Where they live

By convention, agent worktrees live alongside the main checkout:

```
~/dev/sextant/                       # operator's main checkout
~/dev/sextant-worktrees/
  ├── feat-bus-routing-001/          # one agent's worktree
  ├── feat-tui-conversation-002/     # another agent's worktree
  └── fix-clickhouse-migration-003/  # another
```

The manager's `WorktreesRoot` defaults to `~/.local/share/sextant/worktrees/` (`pkg/sextantd/config.go:211`); set `worktree.worktrees_root = "~/dev/sextant-worktrees"` in `sextantd.toml` to match the convention. `worktree.repo_root` must point at the operator's main checkout for the manager to wire up at all.

## Branch naming

Worktree names equal branch names. Pattern (per `conventions/git-workflow.md` and enforced by `pkg/worktree.ValidateName`):

```
<kind>-<short-description>-<seq>
```

| Field             | Allowed values                                            |
|-------------------|-----------------------------------------------------------|
| `kind`            | `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `spec` |
| `short-description` | 2-5 kebab-case words                                    |
| `seq`             | 3-digit counter for collision avoidance                   |

Examples: `feat-bus-routing-001`, `fix-clickhouse-migration-003`, `spec-nats-component-001`.

Agents that get a worktree from the spawn handler get one named `feat-<template>-<short_uuid>-001`. They can later create their own task-shaped worktree via `worktree_create` and move work there.

## CLI

```bash
sextant worktree list
sextant worktree create feat-bus-routing-001 --base main
sextant worktree diff   feat-bus-routing-001 --against main
sextant worktree merge  feat-bus-routing-001 --target main
sextant worktree destroy feat-bus-routing-001 [--force]
```

`--force` on `destroy` overrides the status guard that refuses to destroy non-archived worktrees.

## MCP tools (for agents)

Agents with the right capabilities can manage worktrees through MCP:

| Tool                  | Capability           |
|-----------------------|----------------------|
| `worktree_create`     | `control.worktree`   |
| `worktree_destroy`    | `control.worktree`   |
| `worktree_merge`      | `control.worktree`   |
| `worktree_list`       | `read.worktrees`     |
| `worktree_diff`       | `read.worktrees`     |

## Merging — what happens under the hood

1. Acquire `locks.merge` (bucket `locks`, key `merge`, TTL 5 min). One merge at a time across the whole install.
2. Re-Get the source worktree under the lock to close the TOCTOU window. If status is already `merged`, return success.
3. Mark status `merging`.
4. Remove any stale `.merge-*` worktrees from crashed prior merges.
5. Create a transient worktree at `<worktrees_root>/.merge-<rand>/` on the target branch, in detached HEAD mode.
6. Run `git merge --no-ff <branch>` inside the transient worktree.
7. On conflict: `git merge --abort`, tear down the transient, mark `conflict`, return `{OK: false, Conflicts: [...]}`. The source branch is untouched.
8. On clean merge: update the target ref (`git update-ref refs/heads/<target>`), tear down the transient, mark `merged`, release the lock.

The operator's main checkout is never touched. The merge worktree always tears down (background context — caller cancel can't strand it).

## What the operator's main checkout can do during a merge

Anything. The operator can be on any branch, with any uncommitted state. The merge worktree is independent — it has its own checked-out files and its own HEAD. When the daemon advances the target ref, the operator's working tree is unaffected (a future `git pull` or branch switch will see the new commit).

## Limitations at this snapshot (M14)

- **No remote push.** Merges are local-only. A future milestone wires `git push` (and re-pulls the source before merging).
- **No concurrent merges to different targets.** The single lock serializes everything.
- **No CI gate.** A clean merge result advances the ref unconditionally. Test-gated merges are an M16-era concern.

## Conflict workflow

A `WorktreeMergeResponse{OK: false, Conflicts: [...]}` means the merge ran cleanly aborted. The source branch still has its commits; the operator can:

1. Check out the source worktree.
2. Pull/rebase the target into it.
3. Resolve conflicts.
4. Commit.
5. Retry `worktree_merge`.

The architecture spec describes conflicts surfacing as `user_input.requests` (a "I tried to merge, please resolve" event), but the UX of that isn't wired up yet — today the CLI just returns the conflict report.

## Disk hygiene

`conventions/git-workflow.md` describes archival semantics:

- Idle > 14 days → archive.
- Idle > 30 days → delete.

These rules aren't enforced by `pkg/worktree` itself at this snapshot — the operator (or a future cleanup agent) runs `worktree destroy` manually.

## "Never force-push"

For any branch, in any worktree, force-push is forbidden by convention. Pushed history is contractual. (Reinforced in `conventions/git-workflow.md`.)

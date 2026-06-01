---
id: TASK-27
title: Implement worktree pruning policy — orphan worktrees accumulate on disk
status: Done
assignee: []
created_date: '2026-05-25 14:53'
labels:
  - feature
  - worktree
  - daemon
  - cleanup
  - 'slug:feat-worktree-pruner'
  - P3
  - 'closed:fixed'
dependencies: []
priority: low
ordinal: 27000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

`conventions/git-workflow.md` specifies the pruning policy:

> "Worktrees idle > 14 days → archived (moved to `~/.local/share/sextant/worktree-archive/`)
> Worktrees idle > 30 days → deleted"

No code implements this. After two days of sextant-driven dev, `~/dev/sextant-worktrees/` has 9+ leftover dirs from spawned/killed agents (`feat-default-*`, `feat-lead-*`, `feat-assistant-*`). Each one is a real git worktree consuming disk space + an entry in `.git/worktrees/`.

The expected mechanism: a daemon-side reaper that runs periodically, scans the worktrees root, checks the registry in NATS KV (`worktrees.<name>`), and archives or deletes per the policy.

## Repro

```bash
ls ~/dev/sextant-worktrees/ | wc -l   # 9+ today, growing
cd /Users/lena/dev/sextant-initial && git worktree list | wc -l
# matches — the worktrees are still registered with git, taking up the prunable slot
```

## Impact

- Disk usage grows linearly with agent dispatches.
- `git worktree list` becomes noisy.
- The `worktrees` KV bucket accumulates stale entries (each spawn-flow creates one; nothing deletes them).
- Operators have to manually `git worktree remove <path>` for old ones.

## Proposed fix

`pkg/worktree/pruner.go` (new): a `Pruner` that exposes `Run(ctx, cfg) error`. Wire into sextantd's supervisor loop with a periodic ticker (default every 6h, configurable via `[worktree] prune_interval = "6h"` in `sextantd.toml`).

Per-tick logic:
1. Read the `worktrees` KV bucket for all registered worktrees.
2. For each: read `last_activity` (already a field per `architecture.md` §11). If `last_activity > 30d`, delete: `git worktree remove --force <path>`, drop the KV entry, emit audit envelope `audit.worktree_pruned`. If `> 14d` and `< 30d`, mv to `~/.local/share/sextant/worktree-archive/<original-name>/`, mark KV entry `status=archived`, emit `audit.worktree_archived`.
3. Reconcile: scan `~/dev/sextant-worktrees/` for paths not in the registry — these are KV-orphan worktrees (e.g. created by an old daemon before it crashed). Delete them with `git worktree remove --force` if their last activity satisfies the threshold; otherwise log a warning and leave alone (operator-recoverable).

`last_activity` is updated by the spawn flow (on spawn, on every commit landing via worktree_merge) and by the inactive-worktree timer (the pruner itself doesn't update it — that'd be a cycle).

CLI surface for operators: `sextant worktree prune [--dry-run]` runs the same logic on demand, useful for an "I want this cleaned up now" workflow.

## Acceptance

`TestWorktreePrunerDeletesIdleOver30d`:
1. Bootstrap a sextantd with a few worktrees registered; set their `last_activity` to varied ages (5d, 20d, 40d).
2. Run the pruner.
3. Assert: 40d → deleted (path + KV both gone); 20d → moved to `worktree-archive`; 5d → untouched.

`TestWorktreePrunerHandlesKVOrphans`:
1. Create a worktree dir on disk that has no KV entry; set its mtime to 40d ago.
2. Run pruner.
3. Assert: dir deleted, audit envelope `audit.worktree_pruned_orphan` emitted.

`TestSextantWorktreePruneCLI`: `sextant worktree prune --dry-run` lists what would happen without doing it; `sextant worktree prune` performs the action.

## Related

- `conventions/git-workflow.md` "Disk hygiene" section — policy is already specified
- `specs/architecture.md` §11 — worktree registry shape (last_activity is a field)
- `pkg/worktree/` — current worktree manager; the Pruner lives alongside
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-worktree-pruner.md
Discovered in: post-overnight disk inspection
Original created_at: 2026-05-25T14:53-07:00
Fixed in: f0781e1071afb3fb5ea920caad7478a4a7214808
<!-- SECTION:NOTES:END -->

---
id: TASK-17
title: >-
  worktree_merge advances main but operator's main checkout's working tree lags
  HEAD
status: Done
assignee: []
created_date: '2026-05-25 01:34'
labels:
  - bug
  - worktree
  - operator-workflow
  - 'slug:bug-worktree-merge-leaves-operator-checkout-stale'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 17000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

After `worktree_merge` succeeds, the operator's main checkout shows `git status` markers (M / D) for every file the merge changed. The main branch's ref HAS advanced (visible in `git log --oneline -3`); the working tree just doesn't auto-update to match. The operator has to `git checkout HEAD -- .` (or `git reset --hard HEAD`) to sync.

This is technically the expected git behavior: when a ref moves externally to a checkout, the working tree isn't auto-updated. But it's surprising for operators who watched a merge succeed and then see their `git status` look dirty.

## Repro

Verified after both `dev-3` (Makefile + issue file) and `dev-4` (container_env.go + restart.go + ...).

1. Dispatch a sextant agent to fix an issue. Agent commits on its branch, calls `worktree_merge`, agent reports success.
2. `cd /Users/lena/dev/sextant-initial && git log --oneline -3` — shows the new merge commit.
3. `git status --short` — shows M/D markers for every file in the merge.
4. `git diff --stat` — shows the merge's reverse diff (working tree minus HEAD).
5. `git checkout HEAD -- .` — clears the drift.

## Impact

- **Cosmetic to operator workflow** — operators panic that their checkout is dirty.
- **Catches operators who edit files between merges** — if you'd been editing locally, the post-merge state hides your edits behind the M markers.
- **Compounds with [[bug-restart-no-api-key-forwarding]] and [[feat-doctor-stale-binary-detection]] family**: the operator's checkout reality drifts from the daemon's reality in multiple ways. Each one alone is small; collectively they erode trust in `git status` as the source of truth.

## Architecture context

Per `conventions/git-workflow.md`:

> "The operator's main checkout (typically `/Users/lena/dev/sextant-initial/`) is never touched during a merge — the dedicated transient worktree owns the merge commit, and the target ref advances in the shared `.git` database."

That's intentional. The defect is that there's no follow-on sync mechanism, leaving the operator to figure out the drift.

## Proposed fix (three options)

**Option A — operator-side sync after merge**: `sextant worktree merge` CLI verb (separate from the MCP tool) that wraps the MCP call AND runs `git checkout HEAD -- .` in the operator's main checkout after the daemon-side merge returns ok. Touches only the operator's checkout from the operator's own process, not from the daemon.

**Option B — `sextant doctor` detects it**: doctor reports `working-tree-drift` as a fail/warn when `git status` is non-clean against current HEAD. Pairs nicely with [[feat-doctor-stale-binary-detection]].

**Option C — daemon emits a post-merge event; operator's TUI subscribes and auto-syncs**: longer-term, when a TUI is daily-driving against the operator's checkout. Out of scope until M13-follow-on.

Lean: **B for now, A when there's a `sextant worktree` CLI surface**. B is cheapest (~20 lines of doctor) and immediately useful.

## Acceptance

For Option B:

`TestDoctorFlagsWorkingTreeDrift`:
1. Stub a git repo at known SHA.
2. Externally advance HEAD (simulate worktree_merge).
3. Run `sextant doctor`.
4. Assert a check named `working-tree` returns warn/fail with detail "<n> files differ from HEAD".

## Related

- `conventions/git-workflow.md` — merge flow
- [[feat-doctor-stale-binary-detection]] — sibling operator-checkout-drift detection
- [[feat-make-install-target]] — also operator-side checkout flow
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-worktree-merge-leaves-operator-checkout-stale.md
Discovered in: first two sextant-driven dispatches (dev-3, dev-4)
Original created_at: 2026-05-25T01:34-07:00
Resolved at: 2026-05-25T00:00-07:00
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Bundled with [[feat-doctor-stale-binary-detection]] as paired
operator-checkout-drift detection (Option B from this file's "Proposed
fix" section).

`sextant doctor` gained a `working-tree` check that runs
`git diff --name-only HEAD` in `cfg.Worktree.RepoRoot`. Clean tree
emits `pass`; any drift emits `warn` with detail
`<n> files differ from HEAD; run `git checkout HEAD -- .` to sync`,
giving the operator both the diagnosis and the recovery command in one
row. Silently skipped when `repo_root` is empty or the path isn't a
git checkout. Covered by `TestDoctorFlagsWorkingTreeDrift` and the
clean-tree guard test in `cmd/sextant/doctor_test.go`.

Option A (CLI-wrapper auto-sync) remains the right move once there's
a `sextant worktree` CLI surface; Option C (daemon-pushed sync) is
still M13-follow-on.
<!-- SECTION:FINAL_SUMMARY:END -->

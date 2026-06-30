---
id: TASK-256
title: 'Work-engine: per-run isolated git worktree provisioning'
status: To Do
assignee: []
created_date: '2026-06-30 04:13'
labels:
  - workengine
  - dispatcher
  - worktree
  - P1
  - needs-triage
  - 'slug:feat-per-run-isolated-worktree'
dependencies: []
priority: high
ordinal: 242000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Each run must be bound to its own isolated git worktree+branch — not a scratch dir (current default) and not an operator-pinned shared dir (the live-validation scaffold). The coordinator/dispatcher must provision a fresh worktree per run, thread it to the worker via SEXTANT_PI_WORKDIR, and tear it down after.

Root cause: runDispatch passes only a prompt to the worker; the recipe defaults to a scratch pi-work/<child> dir. The engine never sets SEXTANT_PI_WORKDIR. The live-validation scaffold worked around this by pinning SEXTANT_PI_WORKDIR to a single shared operator-owned worktree — which prevents concurrency and silently contaminates the operator's checkout. Cross-link: [[task-98]], [[feat-work-engine-concurrent-runs]].
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A code/work step dispatched by the managed coordinator runs inside a real git worktree (a branch of the target repo, not a scratch dir), confirmed by the worker's git branch report being a fresh branch name distinct from main. Proof: a managed-path run ends with the worker's git status + branch name in its activity trail, showing a real worktree checkout. Flipper: mechanical. Fake-pass guard: a scratch dir with a .git init is NOT a worktree — must be a linked worktree under git worktree list on the target repo.
- [ ] #2 Two runs dispatched concurrently each receive a distinct worktree+branch — no run shares a worktree path with another. Proof: two parallel managed runs, each producing an independent diff in a different branch; the worktree paths in their activity logs differ. Flipper: operator (live parallel spawn). Fake-pass guard: both runs reaching done does not suffice — their worktree paths must be distinct AND each diff must contain only that run's changes with no cross-contamination (no commits from the other run visible on either branch).
- [ ] #3 The worktree is torn down after the run completes (done or failed), leaving no stray entries in git worktree list. Proof: git worktree list before and after a run shows no net growth. Flipper: mechanical. Fake-pass guard: an orphaned worktree entry that the OS has already deleted still shows in git worktree list — must be pruned.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The fix is in runDispatch (dispatcher) and/or the coordinator's step-launch path: provision a worktree via `git worktree add` against the target repo before spawning the worker, pass the path as SEXTANT_PI_WORKDIR, and register a teardown hook. The target repo path must come from the run/template definition, not an env var the operator sets by hand. Depends on: [[feat-work-engine-managed-coordinator-config]] (TASK-257) — the managed path must be in play before this can be exercised end-to-end.
<!-- SECTION:NOTES:END -->

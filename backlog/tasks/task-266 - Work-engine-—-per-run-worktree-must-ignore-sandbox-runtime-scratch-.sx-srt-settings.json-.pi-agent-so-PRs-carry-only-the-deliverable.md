---
id: TASK-266
title: >-
  Work-engine — per-run worktree must ignore sandbox-runtime scratch
  (.sx-srt-settings.json, .pi-agent/) so PRs carry only the deliverable
status: To Do
assignee: []
created_date: '2026-06-30 07:13'
labels:
  - bug
  - work-engine
  - coordinator
  - worktree
  - sandbox
  - 'slug:bug-work-engine-worktree-sandbox-scratch-pollutes-pr'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 252000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D15, found by a clean live hermetic e2e. The work-engine runs its pi worker in a per-run git worktree (TASK-256; Run.Repo -> branch sxrun/<id>, provisioned by provisionWorktree in clients/coordinator/worktree.go), with the worker's CWD set to that worktree. The srt sandbox + pi runtime write SCRATCH files into the CWD: the recipe (clients/dispatcher/recipes/pi.sh) writes ${WORKDIR}/.sx-srt-settings.json (the computed srt allow/deny profile) and creates ${WORKDIR}/.pi-agent/, into which pi drops an auth.json ({}). D7's worktree-diff capture (clients/pi-bus/src/worktree_diff.ts) sees these as untracked changes, and the trusted-path PR-open step (clients/coordinator/pr.go hostOpenPR, which git add -A commits the worktree's pending changes) sweeps them into the run's commit. RESULT: every work-engine PR's diff carried these stray sandbox files alongside the real deliverable. PROVEN: the hermetic run's PR (github #322) diff = the intended docs/e2e-proof.md PLUS .pi-agent/auth.json + .sx-srt-settings.json. Fix shape: after git worktree add succeeds, make THIS worktree (and only this worktree) ignore the sandbox scratch via git's per-worktree config (extensions.worktreeConfig + a worktree-scoped core.excludesFile pointing at a file inside the worktree's own gitdir) — NOT a committed .gitignore (environment artifacts, not a repo concern) and NOT the shared common-dir info/exclude (which would leak the ignore into the operator's primary checkout). Best-effort: a failed exclude write is logged, never fatal; provisioning still succeeds.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 PROPERTY: a work-engine PR's diff contains ONLY the worker's intended changes — never the sandbox-runtime scratch (.sx-srt-settings.json, .pi-agent/). PROOF: the worktree-exclude unit test (TestProvisionWorktree_IgnoresSandboxScratch in clients/coordinator/worktree_test.go) + a clean managed-run PR whose file list is the deliverable alone. FAKE-PASS GUARD: a test that does not actually create the scratch files in the worktree never exercises the exclude; the test creates both .sx-srt-settings.json and .pi-agent/auth.json AND a real deliverable, and asserts the deliverable IS still seen while the scratch is NOT.
- [ ] #2 PROPERTY: the ignore is scoped to the run's worktree alone — the operator's primary checkout and every other worktree of the repo are UNAFFECTED. PROOF: TestProvisionWorktree_ExcludeIsolatedToWorktree provisions a second worktree of the same repo and asserts it STILL sees the scratch (the exclude did not leak via the shared common-dir info/exclude).
- [ ] #3 PROPERTY: provisioning is no-op-safe — a failed exclude write logs a warn and provisioning still succeeds (the diff merely carries the scratch, the pre-D15 behaviour, never a broken run). PROOF: writeWorktreeExcludes returns its error for the caller to log; provisionWorktree never fails over it; existing worktree tests stay green.
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
In provisionWorktree (clients/coordinator/worktree.go), after git worktree add: resolve the worktree's own gitdir via git rev-parse --absolute-git-dir; git config extensions.worktreeConfig true (idempotent); write the patterns (.pi-agent/, .sx-srt-settings.json) to <gitdir>/info/sx-exclude; git config --worktree core.excludesFile <that file>. Best-effort (log on failure, never fatal). Tests in worktree_test.go.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: clean live hermetic e2e (PR #322 diff carried .pi-agent/auth.json + .sx-srt-settings.json). Fixed in: this PR (fix/task-266-worktree-sandbox-ignore -> rc/work-engine). Related: [[bug-work-engine-sandbox-gitconfig-deny-defeats-d7-capture]] (TASK-265, D14), TASK-256 (per-run worktree), TASK-260 (PR-open step), D7 worktree-diff capture. Note on mechanism: git does NOT honour a per-worktree .git/worktrees/<id>/info/exclude, and rev-parse --git-path info/exclude resolves to the SHARED common-dir exclude — verified — so the fix uses extensions.worktreeConfig + a worktree-scoped core.excludesFile instead.
<!-- SECTION:NOTES:END -->

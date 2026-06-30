---
id: TASK-265
title: >-
  Work-engine — sandbox ~/.gitconfig deny defeats D7 worktree-diff capture (code
  steps block)
status: Done
assignee: []
created_date: '2026-06-30 06:54'
labels:
  - work-engine
dependencies: []
priority: high
ordinal: 251000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A coding step's natural deliverable is a git diff in the worker's worktree; D7 (#313) captures it via captureWorktreeDiff (clients/pi-bus/src/worktree_diff.ts), which shells `git -C <workdir> rev-parse/status/diff` via node execFile. But the work-engine's pi worker runs under the TASK-118 `sandbox` mode, whose profile DENY-READS ~/.gitconfig (clients/dispatcher/recipes/pi.sh denyRead list). So every git invocation exits 128 ("fatal: unable to access '/Users/.../.gitconfig': Operation not permitted"), gitChanges catches it and returns undefined, the step-done run.event reports artifacts:0, and the coordinator's proof gate (correctly) BLOCKS the step. Net: ANY code step whose only deliverable is a git diff (the canon-writing run, TASK-301, etc.) blocks on the live managed path. PROVEN by a hermetic run: the worker wrote a file, its git status hit the .gitconfig deny, the step reported artifacts:0, and the run went `blocked`.

THE PROPERTY: a sandboxed code step whose deliverable is a git diff is captured AND passes the coordinator's proof gate even when ~/.gitconfig is unreadable.

THE FIX (load-bearing): clients/pi-bus/src/worktree_diff.ts — every `execFile("git", ...)` (rev-parse, status, diff, diff fallback) passes an env that forces GIT_CONFIG_GLOBAL=/dev/null and GIT_CONFIG_SYSTEM=/dev/null (merged over the live process.env, computed per call). git status/diff/rev-parse need neither config, so the sandbox deny is never hit; nothing changes for a normal repo. Defense-in-depth: pi.sh also exports the same two vars for the worker's OWN bash git calls (not load-bearing for the gate — the gate depends on captureWorktreeDiff).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 captureWorktreeDiff captures the worktree diff and returns a ref even when the global git config is unreadable (the sandbox ~/.gitconfig deny is reproduced). Proof artifact: the test 'D14: an unreadable global git config does NOT defeat the capture' in clients/pi-bus/test/worktree_diff.test.ts — it points the process env's GIT_CONFIG_GLOBAL at a directory (git exits 128 reading it, the same failure mode as the sandbox deny), asserts a bare git status FAILS under that env (the reproduce-the-deny sanity), then asserts captureWorktreeDiff STILL returns the diff because it forces GIT_CONFIG_GLOBAL=/dev/null. Flipper: mechanical — `cd clients/pi-bus && npm test` is green with the fix; reverting the per-call /dev/null override makes ONLY this test go red (verified: AssertionError 'the diff is captured even though the global git config is unreadable').
- [ ] #2 Fake-pass guard: a test that does NOT make the global config unreadable does not cover D14. The D14 test's bare-git-status sanity assertion (assert.ok(bareFailed)) is the guard — if the surrounding env doesn't actually break plain git, the assertion fails and the test cannot fake-pass. (An earlier draft fake-passed because gitEnv was a module-level snapshot taken at import before the env mutation; the fix computes gitEnv() per call, which is why neutralizing the override now turns the test red.)
- [ ] #3 The normal-repo capture is unchanged: the existing worktree_diff tests (uncommitted-change capture, untracked-file detection, no-change no-op, non-git no-op, empty-workdir no-op) and run_report tests stay green. Proof: full `npm test` in clients/pi-bus shows 52/52 pass. The sandbox is NOT weakened — the fix grants no read access; it makes git not need ~/.gitconfig.
<!-- AC:END -->

---
id: TASK-258
title: >-
  Work-engine: concurrent runs are isolated and both adopted (no collision,
  no owner=none)
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
labels:
  - workengine
  - coordinator
  - concurrency
  - P1
  - needs-triage
  - 'slug:feat-work-engine-concurrent-runs'
dependencies:
  - TASK-256
  - TASK-259
priority: high
ordinal: 244000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Multiple runs in flight simultaneously on the managed path must each be adopted by a coordinator and complete with independent deliverables. The live validation showed a parallel run stalling with owner=none — never adopted by the coordinator — even though the serial run succeeded. Two separate failure modes are likely compounded: adoption reliability (TASK-259) and shared-worktree collision ([[feat-per-run-isolated-worktree]], TASK-256). This ticket is the integration proof that both are fixed together: two runs, both adopted, both done, independent output. Cross-link: [[task-98]], [[feat-run-start-adoption-reliability]] (TASK-259), [[feat-per-run-isolated-worktree]] (TASK-256).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Two runs spawned within 30 seconds of each other on the managed path are BOTH adopted (owner field set, not none) within a bounded time (≤5 minutes of spawn). Proof: the run records for both runs show owner != none within 5 minutes; the activity log shows two distinct coordinator instances or one coordinator that adopted both serially within the window. Flipper: operator (live parallel spawn). Fake-pass guard: the second run must not be silently dropped — owner=none at any point after 5 minutes is a failure regardless of whether the first run completes.
- [ ] #2 Both runs complete with independent deliverables: each produces a diff or artifact that contains only its own changes with no content from the other run. Proof: a side-by-side diff of the two runs' output artifacts showing no shared content and no cross-branch commits. Flipper: operator (live). Fake-pass guard: two runs reaching done status does not satisfy this AC if their worktrees share commits or if one run's artifact contains changes from the other.
- [ ] #3 The managed coordinator does not serialize two concurrent runs by blocking the second's adoption behind the first's completion. Proof: the second run's adoption timestamp precedes the first run's done timestamp (adoption overlaps with active work), OR the coordinator explicitly documents a serial-queue model with a bounded queue depth. Flipper: operator (live timing evidence from activity logs). Fake-pass guard: both runs eventually reaching done does not demonstrate prompt adoption — the second run's owner must be set before the first reaches done.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
This ticket is the capstone integration test for TASK-256 (worktree isolation) and TASK-259 (adoption reliability). It should not be attempted until both prerequisites are green. The concurrent-run scenario directly maps to the production use case of TASK-98: an operator may spawn multiple work tickets simultaneously from the dash.
<!-- SECTION:NOTES:END -->

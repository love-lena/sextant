---
id: TASK-257
title: >-
  Work-engine: managed coordinator + dispatcher run with correct config (no
  manual flags)
status: To Do
assignee: []
created_date: '2026-06-30 04:13'
labels:
  - workengine
  - coordinator
  - managed
  - P1
  - needs-triage
  - 'slug:feat-work-engine-managed-coordinator-config'
dependencies: []
priority: high
ordinal: 243000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The managed workflow + dispatcher components (clients/sextant-cli/internal/components/components.go) must run the work-engine correctly with NO manual flags: a sane per-step timeout (managed currently hardcodes 90s — far too short for a coding step; the scaffold used --step-timeout 30m) and per-run workdir provisioning.

Root cause: the managed coordinator's step timeout is hardcoded to 90 seconds in components.go. A coding step on a real ticket (plan, implement, review) routinely takes 5–30 minutes. The live-validation scaffold worked around this by launching a hand-run coordinator with --step-timeout 30m and a hand-run dispatcher with SEXTANT_PI_WORKDIR pinned — bypassing the managed path entirely. Cross-link: [[task-98]], [[feat-per-run-isolated-worktree]] (TASK-256).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A run dispatched to the MANAGED coordinator (started via `sextant workflow start`, not a hand-run binary) completes a coding step that takes longer than 90 seconds without a timeout error. Proof: a managed-component run log showing a coding step duration >90s with run status reaching the next step, not timing-out. Flipper: operator (live managed run). Fake-pass guard: running a hand-built coordinator binary with a --step-timeout flag in place of the managed component does not satisfy this AC — the managed binary itself must carry the correct timeout.
- [ ] #2 The step timeout is configurable per-workflow or per-step from the workflow definition/template, not a hardcoded constant in components.go. Proof: two workflows with different declared step timeouts, each honoured by the managed coordinator. Flipper: mechanical test. Fake-pass guard: a global env-var override visible only to the operator's shell is not template-level configurability.
- [ ] #3 No manual env-var or flag is required from the operator to run the TASK-98 dev-workflow template on the managed path. Proof: `sextant workflow start <name>` with no extra flags drives a plan+build step to completion. Flipper: operator (live). Fake-pass guard: a README note saying "export SEXTANT_PI_WORKDIR=..." is not "no manual flags".
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Fix site: clients/sextant-cli/internal/components/components.go — replace the hardcoded 90s timeout with a per-step value from the run/template definition. The per-run workdir provisioning fix lives in [[feat-per-run-isolated-worktree]] (TASK-256) and may be co-located here. Check the managed dispatcher's spawn path for other hardcoded assumptions (model, env passthrough).
<!-- SECTION:NOTES:END -->

---
id: TASK-249
title: 'Dash run detail: per-step produced-artifact inline links'
status: To Do
assignee: []
created_date: '2026-06-29 21:15'
labels:
  - ui
  - dash
  - work-engine
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 235000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-244 now records each work step's distinct produced artifact on RunStep.Produced, but the dash run detail does not surface them. Operators can't see/click a step's deliverable.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Dash run detail shows each work step's distinct produced-artifact NAME as an inline link (linkified), verified on a LIVE 2-step run. Proof: open a completed live 2-step run in the dash; each work step row shows its produced artifact as a clickable link resolving to that artifact. Flipper: operator. Fake-pass guard: a run with steps that produced artifacts but render no links fails; the brief artifact alone is not enough.
<!-- AC:END -->

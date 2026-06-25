---
id: TASK-226
title: 'Work-engine: a run can be spawned but never cancelled/stopped'
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - feature
  - workflow
  - work-engine
  - dash
  - ready-for-human
  - 'slug:feat-run-cancel-stop'
  - P2
dependencies:
  - TASK-236
priority: medium
ordinal: 215000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ADR-0048 added an additive stop; the dash defines a terminal cancelled status (workengine.jsx:90) but the Run view exposes no cancel/stop control, and no backend consumes a stop. The only stop tokens in workengine.jsx are subscription teardown and the 'stopping brief' label. The old engine has ctlCancel (apps/workflow/records.go:38, main.go:405) on the old contract. A run created from the new lane cannot be stopped.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The Run view exposes a 'stop run' action
- [ ] #2 Stopping writes status:cancelled to the run record (and/or a control message the coordinator honors) and the coordinator halts the run
- [ ] #3 A stopped run shows terminal in the dash
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Depends on [[feat-run-executor-workflow-run-v1]] (TASK-236). Relates ADR-0048 additive stop.
<!-- SECTION:NOTES:END -->

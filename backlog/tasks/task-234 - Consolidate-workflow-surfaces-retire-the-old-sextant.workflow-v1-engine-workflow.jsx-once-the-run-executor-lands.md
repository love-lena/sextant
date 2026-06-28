---
id: TASK-234
title: >-
  Consolidate workflow surfaces: retire the old sextant.workflow/v1 engine +
  workflow.jsx once the run executor lands
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
updated_date: '2026-06-28 00:36'
labels:
  - chore
  - workflow
  - dash
  - cleanup
  - ready-for-human
  - 'slug:feat-consolidate-workflow-surfaces'
  - P3
dependencies:
  - TASK-236
priority: low
ordinal: 223000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two parallel workflow surfaces coexist in the dash. NEW: workengine.jsx (stage 'workengine', sextant.workflow.run/v1, work/checkpoint/brief steps) — the ADR-0048 contract the redesign ships, with no executor. OLD: workflow.jsx (stage 'workflow', still routed app.jsx:1302,1407-1409) publishes workflow.start, driven by the still-built-and-released cmd/sextant-workflow engine (sextant.workflow/v1, dispatch steps) and violet's mobilizer (mobilize.go:114). They use disjoint types/namespaces (workflow.<id> vs workflow.run.<id>) so they don't corrupt each other, but they are a UX/architecture fork: two formats, one live engine feeding only the old surface. Once the run executor lands, retire the old surface + format.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The old workflow.jsx surface is removed or merged into the work-engine lane
- [ ] #2 cmd/sextant-workflow speaks only the v1 run contract (or the old format is explicitly retained with a documented reason)
- [ ] #3 violet's mobilizer targets the surviving contract
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Depends on [[feat-run-executor-workflow-run-v1]] (TASK-236). Relates [[task-13]], [[task-26]], [[task-192]].

Engine half (delete sextant.workflow/v1 records + the old coordinator path) is the following pass after TASK-236/PR #279: the executor retargeted the coordinator to run/v1 and left the old contract compiled-but-unused (additive). Remaining: remove old Workflow/WorkflowEvent/WorkflowControl/WorkflowStart* records + their tests/vectors/lexicons, plus the workflow.jsx UI half.
<!-- SECTION:NOTES:END -->

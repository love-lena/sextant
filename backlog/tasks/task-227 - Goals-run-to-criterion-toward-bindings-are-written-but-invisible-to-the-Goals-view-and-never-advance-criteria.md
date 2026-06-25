---
id: TASK-227
title: >-
  Goals: run-to-criterion 'toward' bindings are written but invisible to the
  Goals view and never advance criteria
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - feature
  - goals
  - dash
  - workflow
  - ready-for-agent
  - 'slug:feat-goals-toward-run-bindings'
  - P2
dependencies: []
priority: medium
ordinal: 216000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The work-engine writes relates:[{goal,crit,kind:toward}] at spawn (workengine.jsx:346) per ADR-0035/0048. But (a) the goals projection coerces kind to 'proof' or else 'related' (project.ts:87 / project.go:84-87), erasing toward semantics; (b) the Goals view derives 'a run working toward this criterion' purely from c.status===in-progress AND c.owner (goals.jsx:245), not from relations — so a criterion with a live toward-bound run but no manual in-progress flip shows no run; (c) ADR-0048's promise that 'clearing a brief tied to a run advances each criterion it works toward and moves the goal rollup' is unimplemented (the approve->met closeLoop only handles proof relations).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The goals projection surfaces kind:toward as a distinct evidence kind (not flattened to 'related')
- [ ] #2 The Goals view renders runs bound to a criterion via toward relations (not only via in-progress+owner)
- [ ] #3 Clearing/approving a run's brief advances each toward-criterion and moves the goal rollup (this AC depends on TASK-236)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. ACs 1-2 are independently shippable; AC3 depends on [[feat-run-executor-workflow-run-v1]] (TASK-236). Relates ADR-0035, ADR-0048, [[bug-review-consequence-criterion-display]], [[bug-linkworkstream-toggle-noop]].
<!-- SECTION:NOTES:END -->

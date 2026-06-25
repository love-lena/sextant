---
id: TASK-231
title: >-
  Goals: no shared conformance golden for the read-model (Project/GoalView)
  parity
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - feature
  - goals
  - convention
  - testing
  - ready-for-agent
  - 'slug:feat-goals-goalview-conformance-golden'
  - P3
dependencies: []
priority: low
ordinal: 220000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The op-transcript conformance harness replays only setCriterion (conformance_test.go:47-50 / conformance.test.ts:65-73). Project/GoalView parity rests on two separately-authored tests using a hand-copied inline fixture (project_test.go:18-30 vs project.test.ts:21-31) that can silently drift. There is no shared GoalView golden. coequality.test.ts proves write-then-read byte identity but not that Project yields identical GoalView JSON across languages.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A shared projection vector (input artifacts -> expected GoalView[] JSON) exists under protocol/conformance/vectors/goals/
- [ ] #2 Both project_test.go and project.test.ts load it (the way setCriterion.json is shared)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Extends [[task-183]] (vector format+runner), [[task-173]].
<!-- SECTION:NOTES:END -->

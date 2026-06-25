---
id: TASK-229
title: 'Goals: the only write verb setCriterion is unreachable from the Goals UI'
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - feature
  - goals
  - dash
  - ready-for-agent
  - 'slug:feat-goals-setcriterion-ui-control'
  - P2
dependencies: []
priority: medium
ordinal: 218000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The goals convention's single lexicon verb is setCriterion (the only conformance vector, the co-equality proof). The dash exposes window.SX.setCriterion (app.jsx:149-152) but no JSX calls it. Criterion-row actions only open the Spawn-work popover (goals.jsx:265-273). There is no affordance to mark a criterion in-progress/blocked/met/waiting-on-you from the UI; the only persisted status changes are the approve->met closed loop and agents writing over the bus directly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The Goals UI exposes an inline criterion status control that calls SX.setCriterion(goalId, critId, status, headline)
- [ ] #2 Setting a status persists to the goal artifact and re-renders
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. app.jsx:149-152 (verb wired), goals.jsx:265-273 (only spawn). Relates [[feat-goals-toward-run-bindings]].
<!-- SECTION:NOTES:END -->

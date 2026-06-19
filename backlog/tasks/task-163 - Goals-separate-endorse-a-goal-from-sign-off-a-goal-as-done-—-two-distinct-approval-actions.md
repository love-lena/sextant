---
id: TASK-163
title: >-
  Goals: separate 'endorse a goal' from 'sign off a goal as done' — two distinct
  approval actions
status: To Do
assignee: []
created_date: '2026-06-17 23:24'
labels:
  - feature
  - dash
  - goals
  - 'slug:feat-goals-endorse-vs-signoff'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 153000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A goal review currently has one Approve, conflating two different operator intents: (1) ENDORSE — this is a well-formed goal, the right thing to pursue (happens when a goal is proposed); vs (2) SIGN-OFF-DONE — the criteria are met and I'm stamping the goal complete. These are different lifecycle moments and need distinct mechanisms. Lena 2026-06-17 on goal.v0-5-0: 'we need different feedback mechanisms for i approve of this goal being a good goal vs. I approve this goal as in its done and im stamping it off as done.' Needs design in the goals model (goal review.state semantics) + the dash goal-review UX.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A goal review offers two distinct actions: endorse-as-good-goal vs sign-off-as-done
- [ ] #2 The two produce distinct states/events (an endorsed goal is not the same as a done goal)
- [ ] #3 The goal lifecycle/model + dash reflect both (design decides the exact states)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
From Lena 2026-06-17 on the goal.v0-5-0 ratify. Related: TASK-154 (goal feedback box), goals-design, ADR-0035 (goal primitive).
<!-- SECTION:NOTES:END -->

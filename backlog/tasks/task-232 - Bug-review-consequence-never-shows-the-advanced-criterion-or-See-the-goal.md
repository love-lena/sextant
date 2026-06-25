---
id: TASK-232
title: 'Bug: review-consequence never shows the advanced criterion or ''See the goal'''
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - bug
  - dash
  - review
  - goals
  - ready-for-agent
  - 'slug:bug-review-consequence-criterion-display'
  - P2
dependencies: []
priority: medium
ordinal: 221000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-209 (Review consequence — the closed loop) shipped, but its payoff screen never renders. briefTransition is set only when brief.criterion is truthy (app.jsx:464-466), and openBrief (app.jsx:441-445) never populates brief.criterion/brief.goal. So even when submitVerdict's apiReview(approved) -> closeLoop actually advances a proof-linked criterion, the consequence always shows the no-criterion ('run continues') copy — never 'criterion X waiting-on-you -> met' or the 'See the goal' button. Separately, apiReview / SB.setReview returns an advanced[] array (the criteria it moved) that the dash discards (app.jsx:1129-1158).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 openBrief populates brief.criterion/brief.goal from the artifact's relates/proof relations (or submitVerdict feeds the setReview advanced[] return into briefTransition)
- [ ] #2 Approving a brief that advances a criterion shows the transition line and a working 'See the goal' button
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Undercuts [[task-209]] (closed loop shipped but invisible). Verified app.jsx:441-445 vs 464-466. Relates [[feat-goals-toward-run-bindings]].
<!-- SECTION:NOTES:END -->

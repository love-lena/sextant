---
id: TASK-166
title: 'violet: track-until-answered for the routed/delegated case (AC8 follow-up)'
status: To Do
assignee: []
created_date: '2026-06-18 02:22'
labels:
  - feature
  - violet
  - sdk
  - 'slug:feat-violet-track-until-answered'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 156000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
AC8 (every-message-answered) is met for the direct case (violet answers from warm context) in PR #193. GAP (the worker's TODO): when violet ROUTES a message to another owner (spawn/workflow), there's no correlation today linking the spawned agent's eventual reply back to the operator's original message — so 'guaranteed response' for the routed case isn't closed (the answered-watermark only advances on violet's own direct reply). Follow-up: correlate a delegated reply back to the originating operator message (a correlation id on the spawn.request → match the child's reply → mark the original answered). Surfaces with [[goal.violet]] cold-start-work + the unified replies surface (msg.topic.violet.replies / TASK-160).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A message violet routes to a spawned agent/workflow is tracked until that owner's reply lands, then marked answered
- [ ] #2 The correlation survives resume (the response-watermark only advances once the routed reply is in)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
From PR #193 (every-message-answered AC8) worker TODO 2, 2026-06-17. Related: goal.violet AC8, feat-dash-replies-to-you (TASK-160), the Mobilizer seam.
<!-- SECTION:NOTES:END -->

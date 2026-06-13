---
id: TASK-79
title: 'Web dash: flag when an artifact changed since it was approved'
status: To Do
assignee: []
created_date: '2026-06-13 03:34'
labels:
  - feature
  - dash
  - frontend
  - artifacts
  - 'slug:feat-dash-review-staleness'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 84000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D2 records the reviewed revision (review.rev) but has no reliable 'changed since approved' signal: the review write itself bumps the artifact revision, so a plain revision compare false-positives. Distinguishing a content change from a review-metadata change needs more (hash the record content excluding the review block, or store review-state outside the content). Design + implement the staleness signal.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the artifact view flags 'changed since approved' only when CONTENT changed after approval, not when the review block was written
- [ ] #2 mechanism documented (content hash / sidecar / equivalent), does not break the review convention
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up from D2 [[feat-dash-web-ui-d2]] (TASK-71); relates to [[feat-brief-workstream-convention]] (TASK-66) + ADR-0033. Design call on the mechanism.
<!-- SECTION:NOTES:END -->

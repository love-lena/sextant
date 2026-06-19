---
id: TASK-112
title: 'Dash: ''needs review'' should be explicit, not the default artifact state'
status: Done
assignee: []
created_date: '2026-06-15 19:26'
updated_date: '2026-06-15 19:50'
labels:
  - bug
  - dash
  - ux
  - 'slug:bug-dash-needs-review-not-default'
  - P2
  - ready-for-agent
dependencies: []
priority: high
ordinal: 107000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dash currently shows ALL artifacts in the 'needs review' section unless they have review.state='approved'. This inverts the intent — 'needs review' should only appear when explicitly set (review.state='changes-requested' or similar). Artifacts with no review field should appear in the artifact list with no review state indicator, not defaulted into the review queue.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An artifact with no review field renders in the artifact list with no review-state badge
- [ ] #2 Only artifacts with review.state='changes' (or equivalent 'needs review' value) appear in the 'needs review' section
- [ ] #3 Artifacts with review.state='approved' show an approved badge and do not appear in needs-review
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered 2026-06-15: 13 of 17 artifacts showed as needs-review because no review state was set. Related: [[feat-artifact-archive-hide]] (TASK-111).

Nit from orion: 'Reopen' button is now the request-review affordance — consider relabeling to 'Request review' (follow-up, not blocking).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fixed in app.js:236 — statusOf() default changed from 'review' to 'draft'. Dash restarted with --ui dev override. Artifacts with no review state now appear as 'draft'; only explicit review.state='review' surfaces in needs-review. Live on :8765.
<!-- SECTION:FINAL_SUMMARY:END -->

---
id: TASK-80
title: 'Web dash: back goal metrics with a real source'
status: Done
assignee: []
created_date: '2026-06-13 03:34'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - dash
  - frontend
  - 'slug:feat-dash-goals-source'
  - P3
  - ready-for-human
  - superseded
dependencies: []
priority: low
ordinal: 85000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The D2 Home + sidebar 'Goal progress' is a hardcoded stub; there is no bus primitive for goals. Decide the source (a curated goals artifact the assistant maintains, a CI/backlog feed, or a new convention) and wire the dash to it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 goal metrics render from a real source, not a hardcoded stub
- [ ] #2 source is a convention/artifact (no core protocol change) or an explicitly-decided integration
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up from D2 [[feat-dash-web-ui-d2]] (TASK-71, ADR-0033). Likely a curated artifact like the home config.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Superseded by task-173/175/180 under ADR-0041: the goal bus primitive shipped (ADR-0035), the goals contract is fixed in task-173, and the dash speaks goals via the TS conventions in task-180. (The TASK-84 dependency was dangling - replaced by task-173.)
<!-- SECTION:FINAL_SUMMARY:END -->

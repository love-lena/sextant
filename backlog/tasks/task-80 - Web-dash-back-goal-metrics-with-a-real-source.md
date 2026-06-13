---
id: TASK-80
title: 'Web dash: back goal metrics with a real source'
status: To Do
assignee: []
created_date: '2026-06-13 03:34'
labels:
  - feature
  - dash
  - frontend
  - 'slug:feat-dash-goals-source'
  - P3
  - ready-for-human
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

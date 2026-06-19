---
id: TASK-106
title: 'Web dash: header search (⌘K) is dead'
status: To Do
assignee: []
created_date: '2026-06-15 17:03'
labels:
  - bug
  - dash
  - frontend
  - ux
  - 'slug:bug-dash-search-dead'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 101000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The search control in the header shows a tooltip 'Search the bus ⌘K' but clicking it does nothing, ⌘K does nothing, and no input exists in the DOM. Should wire a basic name filter over topics/artifacts, or hide the control until it works.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 ⌘K / clicking the search icon opens a functional search input
- [ ] #2 Search filters topics and artifacts by name as the user types
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Found in dogfood findings artifact (vega, 2026-06-12). Finding #4.
<!-- SECTION:NOTES:END -->

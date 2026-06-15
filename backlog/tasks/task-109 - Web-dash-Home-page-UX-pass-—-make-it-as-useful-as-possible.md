---
id: TASK-109
title: 'Web dash: Home page UX pass — make it as useful as possible'
status: To Do
assignee: []
created_date: '2026-06-15 17:16'
labels:
  - feature
  - dash
  - frontend
  - ux
  - 'slug:feat-dash-home-ux-pass'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 104000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Home is currently functional but not optimised for daily use. As part of the v0.4.0 dash UX pass, audit and improve the Home to be the best possible orientation surface: agenda items that surface what genuinely needs the operator, live cards that are actually informative, layout/hierarchy that makes the current state of the crew immediately readable. The home artifact schema is the backing model — improvements should be reflected in schema + UI together.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The Home agenda surfaces only items that genuinely require the operator (no stale or trivial items)
- [ ] #2 Live cards (agents, goals, activity) show useful real data, not stubs or placeholders
- [ ] #3 The layout and visual hierarchy make crew status readable at a glance
- [ ] #4 The home artifact schema is updated to support any new block types or fields added
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Added to v0.4.0 dash UX pass 2026-06-15. Related: [[feat-goal-progress-bus-primitive]] (TASK-84) — goal blocks on Home need real data once TASK-84 lands.
<!-- SECTION:NOTES:END -->

---
id: TASK-7.4
title: 'Dash: layout engine (presets, toggle, reflow, config)'
status: To Do
assignee: []
created_date: '2026-06-06 03:00'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies:
  - TASK-7.3
references:
  - docs/adr/0021-the-dash-is-a-composable-pane-cockpit.md
parent_task_id: TASK-7
priority: medium
ordinal: 33000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The customization engine (ADR-0021): built-in preset layouts, per-pane toggle on/off, reflow to fill, and a config file that persists the choice — btop-faithful. Operates on the Surface contract (id/title). Detail-on-demand is a hidden pane toggled in/out. The config format leaves room for future free placement without a rewrite.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 preset layouts + per-pane toggle + reflow to fill; config file load/save persists it
- [ ] #2 detail-on-demand: a hidden pane toggles in/out cleanly
- [ ] #3 layout config format leaves room for future free placement
<!-- AC:END -->

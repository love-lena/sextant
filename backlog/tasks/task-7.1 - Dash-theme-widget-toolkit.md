---
id: TASK-7.1
title: 'Dash: theme + widget toolkit'
status: To Do
assignee: []
created_date: '2026-06-06 02:59'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies: []
references:
  - docs/adr/0021-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
parent_task_id: TASK-7
priority: medium
ordinal: 30000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Foundation of the dash library (ADR-0021, ADR-0014), no SDK: a theme package (base16 palette + role-hue tokens + status-by-shape glyphs + the locked keybinding set; light + dark) and the generic Bubble Tea widgets (cursor list, stream viewport, detail pane) that render only from theme tokens. Salvage target from TASK-14.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 theme: base16 + role-hue tokens + status glyphs + locked keybindings; light + dark
- [ ] #2 widgets (cursor list, stream viewport, detail pane) render only from theme tokens, no SDK import
- [ ] #3 teatest goldens + a preview binary; verified in a PTY
<!-- AC:END -->

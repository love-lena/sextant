---
id: TASK-7.4
title: 'Dash: layout engine (presets, toggle, reflow, config)'
status: Done
assignee: []
created_date: '2026-06-06 03:00'
updated_date: '2026-06-10 23:58'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies: []
references:
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
parent_task_id: TASK-7
priority: medium
ordinal: 33000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The customization engine (ADR-0023): built-in preset layouts, per-pane toggle on/off, reflow to fill, and a config file that persists the choice — btop-faithful. Operates on the Surface contract (id/title). Detail-on-demand is a hidden pane toggled in/out. The config format leaves room for future free placement without a rewrite.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 preset layouts + per-pane toggle + reflow to fill; config file load/save persists it
- [x] #2 detail-on-demand: a hidden pane toggles in/out cleanly
- [x] #3 layout config format leaves room for future free placement
- [x] #4 teatest goldens + a VHS `.tape` covering preset-switch, pane-toggle, reflow, and detail-on-demand; the rendered `.gif` attached to the PR
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on feat/dash, PR #99 (https://github.com/love-lena/sextant/pull/99). All acceptance criteria met + verified via two-stage (spec + code-quality) review per subtask. Whole-module `go test ./...` green incl. the no-tag internal/dash e2e; PTY-verified in tmux. Status In Progress pending human sign-off (merge). Commits a4635c8 + 4 review fixes (a34871e..30f84ad).

Fixed in: 4887258 (PR #99)
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #99 (squash 4887258) as part of TASK-7.
<!-- SECTION:FINAL_SUMMARY:END -->

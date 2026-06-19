---
id: TASK-141
title: 'Dash: resizable + collapsible panes (left nav + right artifact-chat rail)'
status: To Do
assignee: []
created_date: '2026-06-16 22:18'
labels:
  - feature
  - dash
  - ux
  - v0.5
  - 'slug:feat-dash-resizable-collapsible-panes'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 131000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (#ui-feedback 2026-06-16): the dash panes should be RESIZABLE and COLLAPSIBLE — specifically the left main navigation (the charcoal sidebar) and the right-side artifact chat / comment rail. Drag-to-resize handles + a collapse toggle per pane, width + collapsed state persisted (localStorage). Lets the operator widen the reading column, collapse the nav for focus, or hide/show the discussion rail. Folds into the v0.5 reskin: the sidebar in the shell, the rail in the Review surface (stage c).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The left nav (sidebar) can be drag-resized + collapsed/expanded; width + state persist across reloads
- [ ] #2 The right artifact-chat / comment rail can be drag-resized + collapsed/expanded; width + state persist
- [ ] #3 Collapsed panes have a clear re-expand affordance; the stage/reading column reflows
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
#ui-feedback (2026-06-16). Folds into the reskin (sidebar=shell, rail=stage-c Review); not urgent — after the current 140/128/sort/112/stage-c batch. Persisted (localStorage) like the dark-mode/split prefs. The design has a fixed 284px sidebar + 344px rail — this makes them adjustable. Claimed via backlog.counter CAS (141).
<!-- SECTION:NOTES:END -->

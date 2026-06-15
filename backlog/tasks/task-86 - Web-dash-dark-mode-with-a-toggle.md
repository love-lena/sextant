---
id: TASK-86
title: 'Web dash: dark mode with a toggle'
status: Done
assignee:
  - '@orion'
created_date: '2026-06-13 04:28'
updated_date: '2026-06-13 05:10'
labels:
  - feature
  - dash
  - frontend
  - ui
  - 'slug:feat-dash-dark-mode'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 91000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Add a full dark theme to the D2 web cockpit (TASK-71) plus a visible toggle button. Today only the sidebar has a charcoal/paper tone tweak; the stage, cards, Home bento, and markdown document view are always light. Dark mode should cover the whole cockpit (stage, topbar, sidebar, artifact/markdown view, Home cards, status pills, buttons) and persist across reloads.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a toggle button switches the whole UI between light and dark (not buried in Tweaks)
- [ ] #2 dark mode covers stage, sidebar, Home, and the markdown document view coherently
- [ ] #3 the choice persists across reloads (localStorage)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Requested by lena 2026-06-12 on frontend-dash. Follow-up on D2 [[feat-dash-web-ui-d2]] (TASK-71). Sidebar already has charcoal tokens; stage/Home/md use hardcoded light colors needing dark overrides.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #125 (squash c50da4090): dark mode (#app.dark theme across stage/sidebar/Home/markdown + a persisted topbar toggle) + Home hot-reload (home config + agent presence added to the 4s poll). Pure CSS+JS, no Go change. Demo 8/8. Not archived.
<!-- SECTION:FINAL_SUMMARY:END -->

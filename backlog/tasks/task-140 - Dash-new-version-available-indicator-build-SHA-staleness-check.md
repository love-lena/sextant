---
id: TASK-140
title: 'Dash: ''new version available'' indicator (build-SHA staleness check)'
status: To Do
assignee: []
created_date: '2026-06-16 21:42'
labels:
  - feature
  - dash
  - build
  - ux
  - v0.5
  - 'slug:feat-dash-new-version-available-indicator'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 130000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (#ui-feedback 2026-06-16): store the current UI build's commit SHA so the dash knows when its loaded build is out of date and shows a quiet 'new version available' nudge — she cmd+r's when ready (NOT auto-reload). Especially valuable now: the live :8765 preview hot-reloads from v0.5 as stages merge and she's guessing when to refresh. Mechanism: embed the build SHA into the served UI at make-ui (esbuild --define / a build.json / a meta tag); the loaded dash compares its embedded SHA against the currently-served build — via a bus artifact dash.build {sha, builtAt} (Lena's suggestion, bus-native) OR a local /api/build endpoint (the minimal version). On mismatch -> a quiet 'new version available — refresh' banner. Optionally wire the :8765 refresh (fetch+reset+make ui) to update the build signal.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The served UI carries its build commit SHA (embedded at build time via make ui)
- [ ] #2 The dash detects when a newer build is being served than the one loaded in the browser
- [ ] #3 On a newer build, the dash shows a quiet 'new version available — refresh' nudge (no forced reload)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
#ui-feedback (2026-06-16). Directly improves the live :8765 v0.5 preview loop (she refreshes manually as stages merge). Mechanism: build-SHA embed + compare vs served (bus artifact dash.build OR /api/build). Related: the :8765 --ui preview loop, #154 build convention (make ui). orion's lane.
<!-- SECTION:NOTES:END -->

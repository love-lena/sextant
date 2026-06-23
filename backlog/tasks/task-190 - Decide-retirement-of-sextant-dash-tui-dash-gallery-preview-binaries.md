---
id: TASK-190
title: Decide retirement of sextant-tui + dash-*gallery preview binaries
status: Done
assignee: []
created_date: '2026-06-23 19:34'
updated_date: '2026-06-23 20:12'
labels:
  - decision
  - dash
  - tui
  - cleanup
  - 'slug:decision-retire-dash-tui'
  - P3
  - ready-for-human
dependencies:
  - TASK-186
priority: low
ordinal: 180000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-up decision spun out of [[feat-dash-standalone-binary]]. With the browser dash promoted to THE dash (sextant-dash) and the terminal cockpit demoted to sextant-tui (deprecated), the open question is whether the cockpit is retired outright (delete the internal/dash TUI code + the dash-layoutgallery/surfacegallery/widgetgallery preview binaries, drop it from build/release) or kept on life support. Deliberately NOT entangled with the extraction so a delete decision does not block the promotion. Note: pkg/tui/widget stays regardless (the chat TUI and other surfaces use it) -- only the dash-cockpit-specific code is in scope.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Decision recorded (keep-deprecated vs retire) with rationale; if retire, a concrete removal plan (which packages/binaries/build+release entries) is listed
- [ ] #2 If retire: pkg/tui/widget and non-dash TUIs are confirmed unaffected
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23 (Lena: 'web dash is THE dash'). Pure decision ticket — tracks the question, not a prescribed answer. Related: [[feat-dash-standalone-binary]].
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Decided 2026-06-23 (design session): the cockpit TUI is NEITHER retired NOR deprecated. It is reframed from 'the dashboard' to a first-class CLI/TUI feature, and its --serve capability is stripped (the web serve path lives only in the new sextant-dash binary). The strip is implemented as part of [[feat-dash-standalone-binary]] (TASK-186); no separate removal work. Both binaries (sextant-dash + sextant-tui) ship via [[feat-dash-release-packaging]]. pkg/tui/widget and the dash-*gallery preview binaries are kept.
<!-- SECTION:FINAL_SUMMARY:END -->

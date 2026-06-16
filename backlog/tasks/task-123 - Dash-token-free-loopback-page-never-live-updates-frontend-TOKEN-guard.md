---
id: TASK-123
title: 'Dash: token-free loopback page never live-updates (frontend !TOKEN guard)'
status: In Progress
assignee: []
labels:
  - bug
  - dash
  - regression
  - slug:bug-dash-tokenfree-no-live-update
  - P1
  - ready-for-agent
dependencies: []
priority: high
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Regression introduced by TASK-115 (token-free loopback, #139). The dash frontend
gates its live SSE stream and its history-seed on a present token:
`app.jsx` had `if(!TOKEN) return;` before opening the `EventSource` (the live
`/api/stream` over `msg.>`), and `if(!TOKEN || seededRef.current) return;` on the
seed effect. When the page is loaded token-free (the whole point of TASK-115),
`TOKEN` is `""`, so the EventSource never opens — the page shows only the initial
REST fetch and does NOT live-update; new messages appear only on a manual refresh.

The SERVER side is correct: `/api/stream` is behind `gate`, and the loopback
exception authorizes it without a token (verified: `curl /api/stream?token=` from
loopback → 200 `text/event-stream`, and a published frame is delivered). The bug
was frontend-only — TASK-115 relaxed the server auth but left the frontend's
token-presence guards in place.

Lena hit this dogfooding v0.4.0 on the token-free dash.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A token-free loopback dash page opens the live SSE stream and updates without a manual refresh
- [x] #2 The history-seed effect runs token-free too (conversation sort seeds on load)
- [ ] #3 A non-loopback page (token in URL) still streams + seeds as before (no regression)
- [ ] #4 Embedded frontend rebuilt; CI green
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Fix: drop the `!TOKEN` conditions in both effects (`app.jsx` ~L151 stream, ~L179
seed) — loopback is token-free so an empty token still streams; a non-loopback
page always carries the token in its URL, so neither path breaks. Recompile via
scripts/build-dash-ui.sh. Hot-fixed live via `--ui` on Lena's dash 2026-06-15;
this ships the embedded fix for v0.4.1. Branch `fix-dash-tokenfree-stream`.
Discovered in: v0.4.0 dogfood. Related: [[bug-dash-js-jsx-drift]] (TASK-121),
[[feat-home-single-next-action]].
<!-- SECTION:NOTES:END -->

---
id: TASK-96
title: 'dash --serve: stable/persisted access token so the URL survives a restart'
status: To Do
assignee: []
created_date: '2026-06-15 01:44'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - dash
  - ux
  - security
  - 'slug:feat-dash-stable-serve-token'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 98000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
sextant dash --serve mints a fresh per-launch bearer token (newToken() in internal/dash/serve.go), so every restart/redeploy changes the URL and the operator's open tab + bookmarks 401 ('my dash broke'). This bit lena repeatedly during the v0.3.1 + history-fix redeploys — each restart forced a new link hand-off. The port is already stable (8765 default); only the token rotates. Give operators a way to keep a stable URL across restarts of the same store.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An operator can get a URL that survives a dash --serve restart (e.g. a persisted token in the per-user store, a --token flag, or a 'current token' the CLI can print)
- [ ] #2 Default behavior + the security tradeoff (a persisted loopback bearer token vs fresh-per-launch) is documented; loopback-only + token-gated posture preserved (ADR-0032)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Recurring friction during redeploys (v0.3.1, the history fix). ready-for-human: persisting a bearer token is a security/UX tradeoff worth a human call. Port already stable at 8765; only the token rotates.

Dash backend model revised by ADR-0041 / task-179 / task-180: the /api/* + SSE + bearer-token + internal/dashapi mechanism described here no longer applies. Re-frame the surviving need against the direct TS NATS-WebSocket client.
<!-- SECTION:NOTES:END -->

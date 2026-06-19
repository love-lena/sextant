---
id: TASK-115
title: 'Dash: remove token requirement for localhost access'
status: To Do
assignee: []
created_date: '2026-06-15 22:45'
labels:
  - bug
  - dash
  - ux
  - ergonomics
  - 'slug:feat-dash-no-token-localhost'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 110000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dash URL carries an access token that invalidates on every restart, forcing the operator to copy a new URL each time. For loopback-only access (127.0.0.1), the token is redundant security — the OS already enforces that only local processes can connect. The server should accept unauthenticated requests from 127.0.0.1, so the dash is always reachable at http://127.0.0.1:8765/ without a token.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 http://127.0.0.1:8765/ opens the dash without a token parameter
- [ ] #2 Token-based access still works (backwards-compat for any existing bookmarks)
- [ ] #3 Remote/non-loopback requests are still gated (token required for non-localhost origins)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
P1: blocks normal operator workflow — URL invalidates on every restart. Loopback access is OS-enforced; token adds no security value for 127.0.0.1.
<!-- SECTION:NOTES:END -->

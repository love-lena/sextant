---
id: TASK-180
title: Dash as a direct NATS-WebSocket co-equal TS client
status: In Progress
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 02:54'
labels:
  - feature
  - dash
  - websocket
  - typescript
  - client
  - 'slug:feat-dash-direct-ws-client'
  - P2
  - ready-for-agent
dependencies:
  - TASK-174
  - TASK-175
  - TASK-177
ordinal: 170000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Make the dash a direct NATS-WebSocket co-equal TS client: enable the bus WebSocket listener; the Go dash backend shrinks to static-SPA host + an ephemeral scoped-credential mint endpoint; the browser uses the TS SDK + TS conventions over wss directly; the dashapi convention re-implementation (hand-rolled goal logic) is removed. Conditional on the task-179 decision. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the bus exposes a WebSocket listener; the browser connects over wss with a dash-minted short-lived scoped credential
- [ ] #2 the dash browser speaks goals/review/home via the TS conventions directly; no Go-backend convention re-implementation remains
- [ ] #3 the Go dash backend is reduced to static hosting + the creds-mint endpoint
- [ ] #4 Draft the ADR revising ADR-0032/0034 + the browser-credential model; signed off on this ticket's merge
- [ ] #5 OPERATOR-VERIFIED: the operator opens the dash in a browser, it connects over wss with a dash-minted short-lived credential, and Home/Goals/review work end-to-end (read live data, write a review verdict, set a goal) with the Go backend reduced to static-host + creds-mint and the internal/dashapi goal/review re-implementation deleted
<!-- AC:END -->

---
id: TASK-179
title: 'Decide: dash as a direct NATS-WebSocket client'
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-19 21:14'
labels:
  - decision
  - dash
  - websocket
  - adr
  - 'slug:decide-dash-direct-ws-client'
  - P2
  - ready-for-human
dependencies:
  - TASK-171
ordinal: 169000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Decision: should the dash browser become a direct NATS-WebSocket co-equal TS client (TS SDK + TS conventions) instead of an HTTP/SSE gateway over the Go backend? It gives the browser the full convention language and kills the dashapi convention re-implementation, but forces browser credential custody (the dash mints short-lived scoped child creds, ADR-0033) + a bus WebSocket listener, and reverses ADR-0032 (browser never touches the bus) - needing its own ADR. Decide adopt/defer and dash-first vs second TS consumer (lean: pi first, dash second). PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a decision is recorded (adopt/defer; sequencing relative to the pi client)
- [ ] #2 if adopt: a new ADR is drafted revising ADR-0032/0034 + the browser-credential model
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Decision: ADOPT the dash as a direct NATS-WebSocket co-equal TS client, sequenced AFTER the pi client (task-177). The ADR revising ADR-0032/0034 + the browser-credential model folds into task-180, drafted there and signed off on its merge.
<!-- SECTION:FINAL_SUMMARY:END -->

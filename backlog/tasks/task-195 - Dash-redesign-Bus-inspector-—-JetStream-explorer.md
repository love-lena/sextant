---
id: TASK-195
title: 'Dash redesign: Bus inspector — JetStream explorer'
status: To Do
assignee: []
created_date: '2026-06-24 00:33'
updated_date: '2026-06-24 01:09'
labels:
  - ready-for-agent
  - lane-bus
dependencies:
  - TASK-220
priority: medium
ordinal: 185000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Bus nav page, JetStream half — a browser-side surface reading the bus directly over the NATS-WS + JetStream API (the dash is stateless; ADR-0044 — no Go relay). Lists streams; stream detail shows header chips + a stats row and tabs Messages / Consumers / Config; the message browser filters by subject, orders newest/oldest, paginates, and expands a row to headers + payload as JSON/Raw/Hex.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Bus page lists streams with name, subjects, message count, storage; active streams marked live.
- [ ] #2 Stream detail: storage/retention/replicas chips, a stats row (messages, bytes, first/last seq, consumers), tabs Messages/Consumers/Config.
- [ ] #3 Message browser: subject filter, newest/oldest order, 'showing X-Y of Z' pagination; a row expands to headers + payload as JSON/Raw/Hex.
- [ ] #4 Consumers tab lists durable/ephemeral + kind, ack policy/wait/max-deliver, and stats.
- [ ] #5 Reads the bus over the browser's own WS connection — no new Go relay endpoint.
<!-- AC:END -->

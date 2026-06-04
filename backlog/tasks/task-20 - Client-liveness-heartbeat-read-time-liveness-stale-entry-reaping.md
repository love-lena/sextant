---
id: TASK-20
title: 'Client liveness: heartbeat + read-time liveness + stale-entry reaping'
status: To Do
assignee: []
created_date: '2026-06-04 04:26'
labels: []
milestone: Future
dependencies:
  - TASK-6
references:
  - docs/adr/0006-wire-atom.md
  - docs/adr/0008-clients-are-processes.md
priority: medium
ordinal: 20000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Extends the clients registry (TASK-6, presence-only) with real liveness. The SDK re-Puts its registry record on a cadence (heartbeat); a reader computes liveness from the bus-stamped revision age vs a threshold (read-time, no daemon/reaper); a sx_clients bucket TTL reaps records left by clients that crashed without a clean Close. Uses the bus clock (trusted, ADR-0006) — no client-reported timestamps. Numbers to settle: cadence (~10s), threshold (~30s = 3x cadence), TTL (~60s).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 SDK heartbeats by re-Putting the registry record every cadence (configurable), stopping on Close/drain
- [ ] #2 Read-time liveness: ClientInfo carries a bus-stamped LastSeen; LiveClients(within) filters to fresh clients
- [ ] #3 Crashed clients (no clean Close) are reaped via a sx_clients bucket TTL
- [ ] #4 Cadence / threshold / TTL defaults chosen and documented
<!-- AC:END -->

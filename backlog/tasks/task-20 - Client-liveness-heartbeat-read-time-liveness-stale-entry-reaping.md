---
id: TASK-20
title: 'Client liveness: heartbeat + read-time liveness + stale-entry reaping'
status: To Do
assignee: []
created_date: '2026-06-04 04:26'
updated_date: '2026-06-12 17:47'
labels: []
milestone: Future
dependencies: []
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
- [ ] #5 Same-identity live-duplicate is detectable: a per-connection fencing nonce in the registry value lets the displaced original fail loud; auto-reclaim of TTL-expired (stale) entries is safe
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Same-identity live-duplicate detection rides here (surfaced in the verb-surface design): two harnesses sharing one creds file silently corrupt presence (overwrite + cross-deregister) and double-deliver. M2 ships block-with --reclaim at connect (TASK-22/TASK-27); the robust fix is a per-connection fencing nonce in the registry value + the heartbeat/TTL here - a live duplicate overwrites the nonce so the original fails loud, and TTL makes auto-reclaim safe (removing the M2 restart friction). Cross-ref TASK-8 (identity).
<!-- SECTION:NOTES:END -->

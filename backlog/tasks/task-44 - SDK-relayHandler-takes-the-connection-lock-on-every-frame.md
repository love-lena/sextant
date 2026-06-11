---
id: TASK-44
title: 'SDK: relayHandler takes the connection lock on every frame'
status: To Do
assignee: []
created_date: '2026-06-10 21:36'
labels:
  - bug
  - sdk
  - performance
  - reconnect
  - 'slug:bug-sdk-relayhandler-per-frame-stats-lock'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 50000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The relay-generation check (PR #99) calls nc.Stats() per delivered frame, which acquires nc.mu — contended on every delivery and blocked behind doReconnect's long-held lock during reconnects (semantically fine: the frame would be dropped anyway, but it stalls the dispatcher). Fix shape: maintain the epoch in an atomic updated by the ReconnectHandler instead of reading nc.Stats() per frame; preserve the verified ordering property (epoch visible before resent subscriptions deliver) — re-verify against nats.go's callback dispatch ordering before relying on the ReconnectHandler, since the current design deliberately avoided it (the handler is async; Stats() under nc.mu is what makes the check race-free). The optimization must not reopen that race.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 No per-frame nc.mu acquisition in the delivery path
- [ ] #2 The exactly-once blip test family still passes at -count=3 under -race
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #99 fold review (2026-06-10), finding 9. CAUTION recorded in description: the naive atomic-epoch swap reopens the race the Stats()-under-lock design closed. Related: [[bug-sdk-reconnect-delivery-inversion]]
<!-- SECTION:NOTES:END -->

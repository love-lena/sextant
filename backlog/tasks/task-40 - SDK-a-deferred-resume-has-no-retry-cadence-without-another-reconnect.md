---
id: TASK-40
title: 'SDK: a deferred resume has no retry cadence without another reconnect'
status: To Do
assignee: []
created_date: '2026-06-10 21:35'
labels:
  - bug
  - sdk
  - reconnect
  - 'slug:bug-sdk-resume-deferral-no-retry-cadence'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 46000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ADR-0027's two-tier resume contract (PR #99) defers a transport-failed resume to 'the next reconnect pass' — but the NATS reconnect cadence only exists while disconnected. If the bus stalls >10s at resume time while the connection stays healthy, the subscription stays registered, emits an ErrResumeDeferred notice promising a retry, and then never delivers again for the life of the connection. Weakens ADR-0027's 'keeps delivering without any action from the caller'. Fix shape: a bounded retry timer armed after a deferral (cleared on successful resume or reconnect), so deferral converges without an external event.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A resume that fails with a transport error on a connection that stays healthy is retried and succeeds without a reconnect occurring
- [ ] #2 Retry is bounded/back-off, not a hot loop; a fatal (bus-replied) error during retry still dies loud per ADR-0027
- [ ] #3 Regression test covers the stalled-bus-healthy-connection path
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #99 fold review (2026-06-10), finding 3. Builds on TASK-39's reconnect work. Related: [[bug-sdk-reconnect-delivery-inversion]]
<!-- SECTION:NOTES:END -->

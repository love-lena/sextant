---
id: TASK-259
title: >-
  Work-engine: run.start adoption reliability — coordinated despite restart or
  concurrent spawn
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
labels:
  - workengine
  - coordinator
  - reliability
  - P1
  - needs-triage
  - 'slug:feat-run-start-adoption-reliability'
dependencies: []
priority: high
ordinal: 245000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A run.start published to msg.topic.run.start must be durably adopted by a coordinator — not missed due to New-only delivery, no live listener at that instant, or a single hand-run instance. The live validation showed a parallel run published to msg.topic.run.start and was never adopted (owner stayed none), while the first run (spawned slightly earlier) was adopted normally. Root cause: a single coordinator instance subscribed with New-only delivery can miss a run.start that arrives while it is occupied with another adoption or step; no retry or durable-queue mechanism ensures every run.start is eventually processed. Cross-link: [[task-98]], [[feat-work-engine-concurrent-runs]] (TASK-258).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A run spawned via the dash while the coordinator is busy processing another run's step is adopted (owner set) within a bounded time (≤5 minutes), not dropped. Proof: spawn a run while a coordinator is mid-step on another run; confirm owner is set within 5 minutes on both runs. Flipper: operator (live). Fake-pass guard: owner must not remain none — "the coordinator was busy" is not an acceptable reason for a run to never be adopted.
- [ ] #2 A run spawned while the coordinator process is restarting (bounced between spawn and the coordinator's next subscription attempt) is adopted on the coordinator's next startup, not lost. Proof: publish a run.start, bounce the coordinator within 10 seconds, confirm the run is adopted on coordinator restart. Flipper: operator (live restart scenario). Fake-pass guard: adoption must use a durable mechanism (e.g. NATS JetStream consumer, not ephemeral New-only subscribe) — a test that only tests the no-restart path does not cover this AC.
- [ ] #3 The adoption mechanism is property-based, not instance-based: a coordinator PROCESS crash and restart does not cause any in-flight run to remain permanently unadopted (owner=none) or permanently stalled. Proof: a coordinator crash during a run causes a restart that re-adopts or continues the run (run reaches done or fails with an explicit error, never stalls silently). Flipper: mechanical or operator (live crash injection). Fake-pass guard: a run that stalls silently with no timeout or error after a coordinator crash FAILS this AC.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Likely requires switching the run.start subscription from ephemeral New-only to a NATS JetStream durable consumer with at-least-once delivery. May also require a coordinator-level adoption CAS to prevent double-adoption when multiple coordinator instances compete. Design must cover the single-coordinator-instance case (most deployments) and the multi-coordinator case.
<!-- SECTION:NOTES:END -->

---
id: TASK-170
title: >-
  Remove the -race quarantine on TestStartConsumer_OneRunPerRequest + fix the
  cross-subtest delivery race
status: To Do
assignee: []
created_date: '2026-06-19 01:05'
labels:
  - test
  - bug
  - workflow
  - 'slug:bug-consumer-test-cross-subtest-race'
  - P2
  - ready-for-agent
dependencies: []
ordinal: 160000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
v0.5.3 temporarily skipped TestStartConsumer_OneRunPerRequest under -race (raceDetectorEnabled guard) to ship a green release. The race is TEST-ONLY (involves testing.T — impossible in the shipped consumer): the test's cooperating subscriptions (test-side dispatcher + ack collector) keep delivering across its t.Run subtests, and NATS waitForMsgs does not wait for an in-flight delivery callback on Unsubscribe/Close, so a delivery goroutine can still be invoking the handler when the next subtest's testing.T bookkeeping runs (race detector flagged messages.go:291). Amplified by the dispatcher subscribing to AND publishing the spawn-ack on the same subject, guaranteeing a queued frame at teardown.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the raceDetectorEnabled skip + raceflag_{race,norace}_test.go are removed
- [ ] #2 TestStartConsumer_OneRunPerRequest runs under -race (no skip) clean across go test -race -count=20
- [ ] #3 fix: drain each subscription before teardown (flush probe) AND/OR give each phase its own bus + unique spawn subject (per-phase isolation, the failsafe)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Diagnosed by codex: NATS in-flight-callback-not-drained on Close + self-ack queue. Candidate Option-A patch (flush probes) staged unverified in the s5-core-rescue worktree. Worker continuing the verified fix. Discovered: v0.5.3 release CI (#222).
<!-- SECTION:NOTES:END -->

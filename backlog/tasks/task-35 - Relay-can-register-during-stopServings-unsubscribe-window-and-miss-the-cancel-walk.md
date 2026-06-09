---
id: TASK-35
title: >-
  Relay can register during stopServing's unsubscribe window and miss the cancel
  walk
status: To Do
assignee: []
created_date: '2026-06-09 19:14'
labels:
  - bug
  - bus
  - shutdown
  - concurrency
  - 'slug:bug-bus-relay-register-shutdown-race'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 41000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Codex review of PR #100 confirmed a narrow pre-existing race in pkg/bus shutdown: stopServing unsubscribes the API subscription, then takes relaysMu and cancels registered relays. An already-spawned handleCall goroutine can call registerRelay between those two steps, observe relaysStopped=false, and register a relay the cancel walk has already passed. That relay is never cancelled by stopServing — it self-exits only when opConn.Close() makes its Publish fail, and may deliver a message or two into a shutting-down bus. The goroutine and registry entry do clean up via defer stopRelay. Pre-exists PR #100: the old relayCancel() design had the identical window.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 No relay can be registered after stopServing has begun its cancel walk (e.g. relaysStopped is set under relaysMu before apiSub.Unsubscribe, or the unsubscribe happens under/inside the same critical section)
- [ ] #2 A regression test (or race-detector-exercised test) covers concurrent registerRelay vs stopServing
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Set relaysStopped (under relaysMu) before calling apiSub.Unsubscribe so late registerRelay calls get the shutting-down error; keep the cancel walk after. Verify no lock-order issue with the backend close path.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Codex review of PR #100 ([[feat-adopt-golangci-lint]]). Not a regression of the registry-of-cancels refactor — the window exists on main too. Related: [[feat-clients-liveness]] (TASK-20) covers the broader relay-liveness gap noted in pkg/bus/bus.go.
<!-- SECTION:NOTES:END -->

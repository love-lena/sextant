---
id: TASK-69
title: >-
  TUI dash should survive a transient connection blip, not exit on 'context
  canceled'
status: To Do
assignee: []
created_date: '2026-06-12 20:43'
labels:
  - bug
  - dash
  - tui
  - resilience
  - reconnect
  - 'slug:bug-dash-survive-transient-reconnect'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 75000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
lena's v0.2.0 TUI dash exited mid-session with 'run: program was killed: context canceled' on a transient bus connection blip the SDK's reconnect machinery rode out (the bus never restarted, was up 12h+, and the dash came back on its own). Observed once, 2026-06-12 ~13:22 (brew v0.2.0). Crew diagnostic ruled out in-flight work — a transient blip the SDK survived but the v0.2.0 dash didn't. The dash should ride out a transient reconnect the way the SDK does (ADR-0027: subscriptions survive a bus restart/blip), not die. Note: the 'program was killed' string is in the v0.2.0 binary but GONE from current main — run/reconnect was reworked post-v0.2.0 (TASK-39 reconnect re-establish, TASK-40 SDK resume cadence) — so current main may already not reproduce it. This is therefore primarily a REGRESSION-TEST + verification ticket, not a known-live fix.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A regression test drives the TUI dash through a transient bus reconnect (a blip, not a full restart) and asserts it stays up and resumes its feed — it does NOT exit with 'context canceled' or otherwise die
- [ ] #2 Confirm current main already survives the blip (post-v0.2.0 run/reconnect rework, TASK-39/40); if it does not, fix the dash to ride out the reconnect. Either way the test pins the behaviour
- [ ] #3 The 'sextant dash --serve' connect path (TASK-68, ADR-0032) has a reconnect-survival test: a transient blip does not kill the local HTTP/SSE server or its single bus client (D1 added none). Follows TASK-68 landing
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Mostly verification + a regression test. Reproduce a transient reconnect against an embedded bus (drop/restore the connection without restarting the store); assert the TUI dash and the --serve server both ride it out and resume. Fix only if current main still dies (the v0.2.0 'program was killed' path appears already reworked). The --serve HTTP server isn't tied to the bus connection, so it likely already inherits SDK resilience — the test confirms it.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: lena's dash crash 2026-06-12 ~13:22 (v0.2.0, brew); crew diagnostic (msg.topic.crew) ruled out in-flight work as cause. The 'program was killed: context canceled' string is present in v0.2.0 but gone from current main. Related: [[bug-sdk-resume-deferral-no-retry-cadence]] (TASK-40), TASK-39 (reconnect re-establish), [[feat-dash-serve-web-api-debug-surface]] (TASK-68, ADR-0032 — the --serve path), TASK-7 (dash TUI). ADR-0027 (subscriptions survive bus restart). Filed by canopus on sirius's delegation, 2026-06-12.
<!-- SECTION:NOTES:END -->

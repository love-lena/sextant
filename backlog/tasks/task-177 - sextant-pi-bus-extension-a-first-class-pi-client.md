---
id: TASK-177
title: 'sextant pi-bus extension: a first-class pi client'
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-19 21:31'
labels:
  - feature
  - pi
  - client
  - typescript
  - 'slug:feat-pi-bus-extension'
  - P2
  - ready-for-agent
dependencies:
  - TASK-175
  - TASK-176
ordinal: 167000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Build sextant-pi-bus: a pi package (in-process TS extension) that makes a pi session a first-class bus client. Holds the TS SDK Client (own scoped creds) opened at session_start / drained at session_shutdown; exposes bus tools (publish/read/subscribe/unsubscribe, artifact ops) and a /set-goal command via the TS conventions; bundles a sextant skill; bridges inbound bus frames to the agent loop (sendMessage triggerTurn); and bridges the agent's actions onto a bus activity topic for the dash. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a pi agent connects as its own scoped identity, is addressable, and wakes on an inbound message
- [ ] #2 bus tools + /set-goal work via the TS conventions (goals set through conv/goals, not hand-rolled)
- [ ] #3 the agent's tool calls + thinking appear on a bus activity topic, viewable in the dash
- [ ] #4 a bundled sextant skill teaches the bus conventions
- [ ] #5 OPERATOR-VERIFIED: with a pi agent on the operator's live bus, the operator DMs it, it wakes and replies, and its tool-calls + thinking stream to the activity topic and render live in the operator's open dash - verified by the operator (or a driven PTY/browser), not a unit test
- [ ] #6 /set-goal actually moves a goal the dash then shows (closes the loop to task-173)
<!-- AC:END -->

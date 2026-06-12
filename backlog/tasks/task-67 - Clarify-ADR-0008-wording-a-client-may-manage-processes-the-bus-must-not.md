---
id: TASK-67
title: 'Clarify ADR-0008 wording: a client may manage processes; the bus must not'
status: To Do
assignee: []
created_date: '2026-06-12 19:43'
labels:
  - chore
  - docs
  - adr
  - 'slug:chore-adr0008-process-mgmt-clarification'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 73000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
lena flagged ADR-0008 wording as misleading (2026-06-12). The line 'a runner calls functions; it never manages processes' reads as a blanket ban on clients managing processes. Intended framing: the BUS never manages processes; clients coordinate with the bus and each other via bus operations, not direct IPC; but a client whose job is managing processes (an M5 dispatcher) launches and talks to its own subprocesses directly, which is in-bounds. Current wording risks the next reader and the M5 dispatcher design mis-reading it as forbidding process-managing clients.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 ADR-0008 distinguishes the bus never manages processes (the bright line) from a client may manage its own processes
- [ ] #2 States explicitly that clients coordinate via bus operations, not direct IPC
- [ ] #3 Reconciles the CLAUDE.md bright-line wording (Call functions, never manage processes or identities) with the clarified framing
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Light wording amendment to ADR-0008 and the derived CLAUDE.md bright-line. Confirm with lena: standalone amendment vs folded into the M5 design ADR.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Provenance: 2026-06-12 planning, lena on msg.topic.orchestration-m5. Canon edit needs human sign-off. Related to the M5 dispatcher design.
<!-- SECTION:NOTES:END -->

---
id: TASK-27
title: 'Run ergonomics + MVP getting-started: bus + a manual client fleet on one host'
status: To Do
assignee: []
created_date: '2026-06-04 18:05'
labels: []
milestone: 'M2: MVP'
dependencies: []
references:
  - docs/adr/0008-clients-are-processes.md
priority: high
ordinal: 26000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The ergonomics to actually run the MVP, plus the end-to-end acceptance walkthrough (M2 DoD). Per ADR-0008: a low-barrier way to run clients and a dev launcher. Plus a documented loop proving manual-comms works: start the bus, manually start >=2 clients (a BYO harness via the MCP/skill + the dash, or two harnesses), they exchange messages and share artifacts, a human observes via the dash — no dispatcher, no coordinator.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant run <file> runs a single handler file as a client (ADR-0008 A2)
- [ ] #2 sextant up --with-dir ./clients/ launches a directory of clients (launch-and-forget, no supervision) (ADR-0008 B)
- [ ] #3 Getting-started doc: bus up + manually start >=2 clients that exchange messages + share artifacts, human observes via the dash
- [ ] #4 The end-to-end manual-comms loop works on a single host with no dispatcher/coordinator
<!-- AC:END -->

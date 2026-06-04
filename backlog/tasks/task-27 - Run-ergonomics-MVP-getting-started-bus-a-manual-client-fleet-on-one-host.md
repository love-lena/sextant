---
id: TASK-27
title: 'Run ergonomics + MVP getting-started: bus + a manual client fleet on one host'
status: To Do
assignee: []
created_date: '2026-06-04 18:05'
updated_date: '2026-06-04 21:39'
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
The ergonomics to run the MVP plus the e2e acceptance walkthrough (M2 DoD). Per ADR-0008: a low-barrier way to run clients and a dev launcher. Identity: each client gets its OWN creds (sextant token <id>) - never share a creds file; a duplicate live identity is blocked at connect with --reclaim (ADR-0012; robust auto-reclaim -> TASK-20). Getting-started doc: bus up, manually start >=2 clients (a BYO harness via the MCP/channel + skill, two harnesses, or CLI clients), they exchange messages + share artifacts, observed via the CLI (subscribe/read) and/or the MCP channel - no dispatcher, no coordinator, no dash (dash moved to M4). Full design: .work/rfcs/rfc-m2-verb-surface.md.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Per-harness creds are the main lever against same-identity collisions; connect-time block-with --reclaim is the stopgap, robust dedup is TASK-20. Observation surface is the CLI + MCP channel (the dash is M4).
<!-- SECTION:NOTES:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant run <file> runs a single handler file as a client (ADR-0008 A2)
- [ ] #2 sextant up --with-dir ./clients/ launches a directory of clients (launch-and-forget, no supervision) (ADR-0008 B)
- [ ] #3 Each client uses its own minted creds; a duplicate live identity is blocked at connect with --reclaim (ADR-0012)
- [ ] #4 Getting-started doc: bus up + >=2 manual clients exchange messages + share artifacts, observed via the CLI (subscribe/read) and/or the MCP channel
- [ ] #5 The end-to-end manual-comms loop works on a single host with no dispatcher/coordinator/dash
<!-- AC:END -->

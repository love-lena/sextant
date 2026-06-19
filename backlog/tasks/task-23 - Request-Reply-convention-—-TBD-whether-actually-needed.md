---
id: TASK-23
title: Request/Reply convention — TBD whether actually needed
status: To Do
assignee: []
created_date: '2026-06-04 17:52'
updated_date: '2026-06-19 21:42'
labels: []
milestone: Open design questions
dependencies: []
references:
  - docs/adr/0001-vision.md
  - docs/adr/0003-high-level-architecture.md
  - docs/adr/0011-workflows.md
priority: low
ordinal: 22000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Request/Reply is named as a convention in ADR-0001/0003 (fan-out/join = Promise.all over request/reply; synchronous asks). PLACEHOLDER — might be needed, might not. Do not build speculatively. Messages + Artifacts + subscriptions may already cover the real use cases; request/reply, if needed, is likely a thin SDK helper (reply subject + correlation id), not a new primitive. Revisit when a reference client (coordinator/dispatcher) hits a concrete synchronous-ask need that pub/sub can't serve cleanly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 TBD: keep parked until a concrete need surfaces in a reference client
- [ ] #2 If adopted: an SDK helper (reply subject + correlation id), not a new primitive
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
If revisited, frame against ADR-0041's library-default vs reference-client-for-single-writer split (task-26 concluded request/reply not needed).
<!-- SECTION:NOTES:END -->

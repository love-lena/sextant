---
id: TASK-250
title: 'Confinement coverage: real-pi proof for read-only file tools grep/find/ls'
status: To Do
assignee: []
created_date: '2026-06-29 21:19'
labels:
  - work-engine
  - security
  - test
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 236000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
From TASK-118: gate.ts classifyConfinement covers read/edit/write/grep/find/ls, but only `read` is proven against the REAL pi binary; grep/find/ls confinement is unit-only. Close the real-pi coverage gap.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A real pi worker TOLD to grep/find/ls OUTSIDE its scoped dir is DENIED (not merely un-attempted). Proof: an against-the-real-pi test for each of grep/find/ls shows the out-of-scope access blocked. Flipper: mechanical + operator. Fake-pass guard: unit-only coverage does not count — must exercise the real pi binary.
<!-- AC:END -->

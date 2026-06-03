---
id: TASK-2
title: 'Wire atom in the SDK: JSON envelope + epoch + ULID skew validation'
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies: []
references:
  - docs/adr/0006-wire-atom.md
  - docs/adr/0010-lifecycle-and-versioning.md
priority: high
ordinal: 2000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The {id,sender,kind,epoch,record} envelope; per-message epoch; sender+receiver ULID-vs-bus-ts skew check; epoch hard-gate at connect. Governed by ADR-0006, ADR-0010.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Envelope shape {id,sender,kind,epoch,record}; record is JSON
- [ ] #2 Sender and receiver enforce |ULID.ts - bus ts| <= 5min (quarantine+flag)
- [ ] #3 Epoch read + exact-matched at connect; mismatch fails loud
<!-- AC:END -->

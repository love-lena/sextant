---
id: TASK-252
title: >-
  run/v1 proof-contract doc-parity: TS RunEvent.artifacts lacks the
  typed-proof-channel doc
status: To Do
assignee: []
created_date: '2026-06-29 22:57'
labels:
  - docs
  - conformance
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 238000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Discovered during TASK-243: the Go RunEvent now treats artifacts as the typed proof channel the deterministic gate decides from, but conventions/workflow/ts/src/run.ts RunEvent has no matching comment. Cross-language doc drift.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 TS RunEvent carries the same doc as the Go side: 'artifacts = mechanically-collected produced-artifact metadata; the deterministic gate decides ONLY from it (existence-checked), never brief prose.' Proof: the comment exists on both; a doc-parity check (or review) confirms. Flipper: mechanical/review. Fake-pass guard: a comment on only one language does not count.
<!-- AC:END -->

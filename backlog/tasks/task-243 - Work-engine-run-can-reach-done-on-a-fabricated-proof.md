---
id: TASK-243
title: Work-engine run can reach "done" on a fabricated proof
status: To Do
assignee: []
created_date: '2026-06-29 02:41'
labels:
  - bug
  - workengine
  - coordinator
  - P1
  - needs-triage
  - 'slug:bug-workengine-fabricated-proof-passes-gate'
dependencies: []
priority: high
ordinal: 230000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A run reached status=done while its stopping brief claimed a deliverable artifact that was never produced. Evidence: run 01KW8J2NNZZA844WA5GDGDTJW8 ("Write a poem for next week"). The brief artifact's proof_of_completion + artifacts_to_report named `poem-for-next-week`, but that artifact does not exist (the poem lived only inside the brief's poem_text). The coordinator's brief stop-gate only checks that the brief step produced >=1 artifact (the brief itself); it trusts the brief's self-reported deliverables and cannot validate them without reading opaque content (content-opacity bright line). So a brief that lies about its artifacts still passes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A run cannot go done while the brief references (as proof/deliverable) an artifact that was not actually produced by the run's workers
- [ ] #2 The gate decides only from run.event-reported artifact metadata (names/kinds/versions the worker declares it produced), never by parsing the brief body
- [ ] #3 Regression: a run whose brief claims a non-existent artifact ends blocked, not done
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Workers report ALL produced artifacts in their run.event metadata; the coordinator validates the brief's claimed proof artifacts against that reported set (and/or against artifact existence on the bus). Respects content-opacity — substrate never reads the brief body. Design call on how strictly to match claims vs reported set.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28 (session). Relates to the run executor (task-236) brief stop-gate. The just-shipped gate fix (PR #283) made the gate kind-independent (step-boundary) but does NOT validate claimed deliverables — this is the deeper hole.
<!-- SECTION:NOTES:END -->

---
id: TASK-243
title: Work-engine run can reach "done" on a fabricated proof
status: To Do
assignee: []
created_date: '2026-06-29 02:41'
updated_date: '2026-06-29 21:16'
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
- [ ] #4 Shape-independent proof: a run whose brief declares its deliverable under ANY key the gate does not hardcode (deliverables[], free-text poem_text, proof string, etc.) STILL cannot reach done unless the declared proof artifact actually exists. The gate derives proof refs from a TYPED run.event metadata field, NEVER by matching a hardcoded set of brief-body keys (closes PROBE C). Proof: a real-bus test where the brief names its deliverable under an unrecognized key and no artifact is produced -> run BLOCKS. Fake-pass guard: adding the new key to a hardcoded allowlist does NOT count — per AC #2 the gate must not parse brief-body shapes at all.
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Workers report ALL produced artifacts in their run.event metadata; the coordinator validates the brief's claimed proof artifacts against that reported set (and/or against artifact existence on the bus). Respects content-opacity — substrate never reads the brief body. Design call on how strictly to match claims vs reported set.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28 (session). Relates to the run executor (task-236) brief stop-gate. The just-shipped gate fix (PR #283) made the gate kind-independent (step-boundary) but does NOT validate claimed deliverables — this is the deeper hole.

ADVERSARIAL QA 2026-06-29 (DoD gate): #286 partially fixes this. CLOSED: the worst hole — a worker self-declaring a phantom artifact in run.event metadata is caught (gate does GetArtifact existence check; real-bus TestRun_BlocksOnFabricatedProof passes; PROBE A blocks). UNMET: verifyDeclaredProof (coordinator/main.go:407-426) parses the OPAQUE brief body via workflow.DeclaredProofArtifacts, which recognizes EXACTLY three keys (proof_artifacts[], proof_of_completion.artifact, artifacts_to_report[].name). PROBE C: a brief naming its deliverable under any other key -> extractor returns [] -> proof check SKIPPED -> done with no deliverable (the 01KW8J2N failure class recurs on key-drift). This also violates AC #2 (never parse the brief body). ACCEPTED by-design (NOT a defect): PROBE B — existence-only check passes when the artifact exists but its content is wrong; content correctness is the review layer's (TASK-242) job, not the deterministic gate (content-opacity). FIX DIRECTION: declare proof/deliverable refs as a TYPED field in run.event metadata; gate existence-checks those refs and stops parsing brief-body shapes entirely. SERIALIZE after TASK-244 (collides on coordinator/main.go + run.go).

Content-opacity boundary (operator clarification 2026-06-29): 'decide from metadata, never parse the brief body' binds the PROGRAMMATIC/deterministic coordinator ONLY — the proof-gate + run-envelope single-writer, which must use typed run.event metadata and never read artifact content. The opt-in AGENT-MODE coordinator (TASK-242) is a convention CLIENT acting as an agent and DOES read produced content — judging acceptance / edit-vs-redo IS reading the deliverable. So TASK-243 AC2 constrains the deterministic gate, NOT the agent reviewer; PROBE B (content wrong but artifact exists) is the agent reviewer's job (TASK-242), not a deterministic-gate defect.
<!-- SECTION:NOTES:END -->

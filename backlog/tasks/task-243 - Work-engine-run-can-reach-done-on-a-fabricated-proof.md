---
id: TASK-243
title: Work-engine run can reach "done" on a fabricated proof
status: Done
assignee: []
created_date: '2026-06-29 02:41'
updated_date: '2026-06-29 23:20'
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
- [x] #1 A run reaches done ONLY when every produced-artifact the workers MECHANICALLY reported in run.event metadata EXISTS (existence-checked via GetArtifact); the deterministic coordinator decides SOLELY from that typed metadata and never reads/parses brief content. Combined with TASK-244's hollow-step gate (no work step may report zero artifacts), done always corresponds to real, existence-verified deliverables. Proof: a real-bus run where a worker reports a typed produced-artifact ref with no matching artifact -> BLOCKED. Flipper: mechanical + operator. Fake-pass guard: a deliverable named only in brief PROSE is intentionally NOT gated here (content-opacity -> TASK-242 reviewer; real deliverables covered by TASK-244); there is no brief-body parse and no key allowlist to extend.
- [x] #2 The gate decides only from run.event-reported artifact metadata, NEVER by parsing the brief body. Proof: code inspection shows the brief-body parse (DeclaredProofArtifacts hardcoded-key match) is DELETED; a test confirms the gate issues no brief-content read on the decision path. Flipper: mechanical. Fake-pass guard: any reintroduction of brief-body key matching fails this.
- [x] #3 Regression: a run whose worker reports a TYPED produced-artifact that does not exist on the bus ends BLOCKED, not done. Proof: TestRun_BlocksOnFabricatedProof rewritten to emit the phantom in the typed artifacts field (no matching artifact) -> blocked; RED on a build that skips the existence-check, GREEN with it. The prior brief-body-parse version is intentionally retired (documented contract change). Flipper: mechanical. Fake-pass guard: the phantom rides the typed metadata channel, not prose, so no brief-key shape can make it pass.
- [x] #4 Shape-independent: there is NO brief-body parse and NO key allowlist; the gate reads only the typed produced-artifact metadata, so no brief-body key shape can make a run pass or skip the check. Proof: a brief naming its deliverable under an UNRECOGNIZED prose key, with the real work-step artifact produced, reaches done legitimately (gate ignores the key); and the phantom-in-typed-metadata case blocks (AC #3). Fake-pass guard: 'add the new key to an allowlist' is impossible — no allowlist exists.
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Workers report ALL produced artifacts in their run.event metadata; the coordinator validates the brief's claimed proof artifacts against that reported set (and/or against artifact existence on the bus). Respects content-opacity — substrate never reads the brief body. Design call on how strictly to match claims vs reported set.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28 (session). Relates to the run executor (task-236) brief stop-gate. The just-shipped gate fix (PR #283) made the gate kind-independent (step-boundary) but does NOT validate claimed deliverables — this is the deeper hole.

ADVERSARIAL QA 2026-06-29 (DoD gate): #286 partially fixes this. CLOSED: the worst hole — a worker self-declaring a phantom artifact in run.event metadata is caught (gate does GetArtifact existence check; real-bus TestRun_BlocksOnFabricatedProof passes; PROBE A blocks). UNMET: verifyDeclaredProof (coordinator/main.go:407-426) parses the OPAQUE brief body via workflow.DeclaredProofArtifacts, which recognizes EXACTLY three keys (proof_artifacts[], proof_of_completion.artifact, artifacts_to_report[].name). PROBE C: a brief naming its deliverable under any other key -> extractor returns [] -> proof check SKIPPED -> done with no deliverable (the 01KW8J2N failure class recurs on key-drift). This also violates AC #2 (never parse the brief body). ACCEPTED by-design (NOT a defect): PROBE B — existence-only check passes when the artifact exists but its content is wrong; content correctness is the review layer's (TASK-242) job, not the deterministic gate (content-opacity). FIX DIRECTION: declare proof/deliverable refs as a TYPED field in run.event metadata; gate existence-checks those refs and stops parsing brief-body shapes entirely. SERIALIZE after TASK-244 (collides on coordinator/main.go + run.go).

AC1/AC3 reworded 2026-06-29 to the chosen design (option A): proof = MECHANICALLY-collected typed produced-artifact metadata, existence-checked; the brief-body parse is deleted entirely (not relocated). The fabrication class is closed by the COMBINATION of TASK-244's hollow-step gate (real work-step artifacts) + this existence check. A brief-prose-only false claim is intentionally not gated by the deterministic coordinator (TASK-242 reviewer scope). Chose A over a typed proof[] field (B) because B reintroduces a gameable self-declared channel; A is mechanical + ungameable.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shape-independent proof gate (option A): the deterministic coordinator decides solely from mechanically-collected typed produced-artifact metadata, existence-checked on EVERY step (work + brief) via a distinct existsArtifact probe; brief-body parsing deleted (no key allowlist). The 01KW8J2N fabrication class is closed by the COMBINATION of TASK-244's count gate (no hollow step) + this existence gate (no phantom ref at any step). Prose-only claims route to the TASK-242 reviewer (content-opacity). Independent adversarial QA found a PROBE-A escape in the first cut (work-step artifacts existence-checked nowhere); fixed + regression-tested; AC#3 content-opacity confirmed STRENGTHENED (asserts !Read AND ExistsProbed). Merged to rc via #300.
<!-- SECTION:FINAL_SUMMARY:END -->

---
id: TASK-263
title: >-
  Work-engine: ship D7 + complete D8 fix to the managed/released path (binaries
  must carry both)
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
labels:
  - workengine
  - release
  - P1
  - needs-triage
  - 'slug:chore-ship-d7-d8-to-managed-released-path'
dependencies:
  - TASK-264
priority: high
ordinal: 249000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D7 (step-done race fix, PR #313, MERGED to rc/work-engine) and D8 (independent skill-armed verify step, PR #314, pending merge) must be merged AND included in a released version so the MANAGED components carry them. The live validation worked around their absence by hand-copying the D7 pi-bus bundle and hand-building a custom D8 coordinator — neither workaround is present in any released binary.

D8 is NOT shippable until [[fix-verify-worker-blocked-outcome-signal]] (TASK-264) also lands in PR #314: without it, the verify gate is dead code (the verifier worker has no path to emit outcome:'blocked', so a broken deliverable still reaches done). Shipping D8 without TASK-264 ships a verify step that NEVER blocks. D7 is independently shippable. Cross-link: [[task-98]], [[fix-verify-worker-blocked-outcome-signal]] (TASK-264).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The released managed dispatcher binary's embedded pi-bus package contains the D7 step-done fix: a step is not marked done until its artifact stamps are complete. Proof: a version check (`sextant version`) shows a version that includes PR #313; a managed run exercising a step with artifacts does NOT exhibit the step-done race (artifacts are present in the proof-gate check). Flipper: operator (version check + live run). Fake-pass guard: "PR #313 is merged to rc/work-engine" is not sufficient — the released binary (installed via brew) must carry it; checking the release tarball's embedded pi-bus bundle confirms it.
- [ ] #2 The released managed coordinator supports the complete D8 verify step kind: a workflow template with a verify step, when the verifier concludes DoD-not-met, drives the run to `blocked` status — not `done`. Proof: a managed run with a verify step on a broken deliverable ends in `blocked` status via the REAL reporter→outcome path. Flipper: operator (live managed run). Fake-pass guard: a verify step that runs and reaches done regardless of the verifier's conclusion does NOT satisfy this AC — the blocked path must exercise the real sextant_run_block→RunReporter path (TASK-264), not the verifyDispatcher test fake. "PR #314 is merged" is not sufficient — the complete D8 fix (including TASK-264) must ship.
- [ ] #3 The operator update is complete: the brew-installed CLI is the version carrying both D7 and the complete D8 fix, confirmed by the operator running `sextant version` and observing the expected version string. Flipper: operator. Fake-pass guard: a version check on a hand-built binary from the source tree does not confirm the released brew-installed CLI is updated.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Steps: (1) Land TASK-264 fix in PR #314 (feat/d8-verify-step). (2) Merge PR #314 into rc/work-engine (after TASK-264 is complete). (3) Merge rc/work-engine into the release branch. (4) Cut a release tag (operator signs off — classifier blocks bus-authorized push). (5) Update the Homebrew formula. (6) Operator runs `brew upgrade sextant` and confirms version. Per project convention: tags need operator sign-off; never push release tags without Lena's approval. D7 (PR #313) is already merged and can ship independently if a patch release is needed before D8 is ready.
<!-- SECTION:NOTES:END -->

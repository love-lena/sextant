---
id: TASK-263
title: >-
  Work-engine: ship D7 + D8 to the managed/released path (binaries must carry
  both fixes)
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
labels:
  - workengine
  - release
  - P1
  - needs-triage
  - 'slug:chore-ship-d7-d8-to-managed-released-path'
dependencies: []
priority: high
ordinal: 249000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D7 (step-done race fix, PR #313, MERGED to rc/work-engine) and D8 (independent skill-armed verify step, PR #314, pending merge) must be merged AND included in a released version so the MANAGED components carry them. The live validation worked around their absence by hand-copying the D7 pi-bus bundle into the coordinator's embed path and hand-building a custom D8 coordinator — neither of these workarounds is present in any released binary.

A managed run on the current released CLI will exhibit the D7 step-done race (step marked done before artifacts are stamped, proof-gate sees 0 artifacts) and will have no verify step capability. The capstone proof (TASK-98 AC#10) requires the MANAGED path, which requires the released binaries to contain both fixes. Cross-link: [[task-98]].
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The released managed dispatcher binary's embedded pi-bus package contains the D7 step-done fix: a step is not marked done until its artifact stamps are complete. Proof: a version check (`sextant version`) shows a version ≥ the release that includes PR #313 + #314; a managed run exercising a step with artifacts does NOT exhibit the step-done race (artifacts are present in the proof-gate check). Flipper: operator (version check + live run). Fake-pass guard: "PR #313 is merged to rc/work-engine" is not sufficient — the released binary (installed via brew) must carry it; checking the release tarball's embedded pi-bus bundle contents is the positive proof.
- [ ] #2 The released managed coordinator supports the verify step kind (D8): a workflow template with a verify step runs to completion under the managed coordinator without an "unknown step kind" error. Proof: a managed run with a verify step in the template reaches the verify step and executes it. Flipper: operator (live managed run with verify step). Fake-pass guard: "PR #314 is merged" is not sufficient — the managed coordinator binary (not a hand-built one) must execute the verify step.
- [ ] #3 The operator update is complete: the brew-installed CLI is the version carrying both D7 and D8, confirmed by the operator running `sextant version` and observing the expected version string. This is an operator AC. Flipper: operator. Fake-pass guard: a version check on a hand-built binary from the source tree does not confirm the released brew-installed CLI is updated.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Steps: (1) Merge PR #314 (D8 verify step) into rc/work-engine. (2) Merge rc/work-engine into the release branch. (3) Cut a release tag (operator signs off per Lena's release process — classifier blocks bus-authorized push). (4) Update the Homebrew formula. (5) Operator runs `brew upgrade sextant` and confirms version. Per project convention: tags need operator sign-off; never push release tags without Lena's approval.
<!-- SECTION:NOTES:END -->

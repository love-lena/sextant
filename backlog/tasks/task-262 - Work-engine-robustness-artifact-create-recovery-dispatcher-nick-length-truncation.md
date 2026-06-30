---
id: TASK-262
title: >-
  Work-engine robustness: artifact create-recovery + dispatcher nick length
  truncation
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
labels:
  - workengine
  - bug
  - dispatcher
  - P2
  - needs-triage
  - 'slug:fix-work-engine-robustness-artifact-nick'
dependencies: []
priority: medium
ordinal: 248000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two independent robustness bugs found during the live validation scaffold:

(a) **Artifact create-recovery (D5):** A worker's sextant_artifact_put call in create mode on a pre-existing artifact name fails with an error (the bus rejects create on an existing name) and the worker does not fall back to update mode. The step completes but reports 0 artifacts. When the programmatic proof-gate (TASK-243) checks reported artifacts, it finds none and blocks the run with a false-positive failure. A worker that successfully writes content but loses the artifact reference is indistinguishable from one that never wrote anything.

(b) **Dispatcher nick length (D6):** The dispatcher uses the step LABEL string as the display name (nick) for the minted child client. The bus enforces a maximum nick length (128 chars). A template with a descriptive step label longer than 128 chars causes a hard dispatch failure — the child is never minted and the step never starts. Cross-link: [[task-98]].
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 (Artifact create-recovery) A work step that calls sextant_artifact_put in create mode on a name that already exists (from a prior run or a retry) succeeds by falling back to update mode, and the step reports the artifact as produced. Proof: repro the failure (create on existing name → 0 artifacts → proof-gate block), then verify the fix: the step reports the artifact, the proof-gate passes, and the artifact's content matches the worker's latest write. Flipper: mechanical test (RED→GREEN on the repro). Fake-pass guard: a test that only uses a guaranteed-fresh artifact name does not cover this — the test must use a name that pre-exists.
- [ ] #2 (Dispatcher nick length) A template with a step label longer than 128 characters dispatches successfully — the dispatcher truncates or slugifies the nick to fit the bus limit. Proof: a template with a step label of ≥200 chars dispatches, mints a child, and the step starts. Flipper: mechanical test (RED→GREEN on a long-label template). Fake-pass guard: a test that only uses short step labels does not cover this — the test must use a label that exceeds 128 chars.
- [ ] #3 Both fixes are covered by regression tests that are RED before the fix and GREEN after, preventing reintroduction. Proof: test names and RED→GREEN evidence in the PR. Flipper: mechanical (CI). Fake-pass guard: tests added only after the fix (no RED baseline recorded) provide weaker regression coverage — the PR must show the tests failing on the unfixed code.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
(a) Fix site: the pi worker's sextant_artifact_put handler or the MCP tool wrapper — add a create-or-update semantic: try create, on conflict retry with update. Alternatively, expose a dedicated upsert mode. (b) Fix site: the dispatcher's nick/display-name construction — truncate to 128 chars, preserving enough to be human-readable (e.g. first 120 chars + "…"). Both are self-contained fixes with no cross-component dependencies.
<!-- SECTION:NOTES:END -->

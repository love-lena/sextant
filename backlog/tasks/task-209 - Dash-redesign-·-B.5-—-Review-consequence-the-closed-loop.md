---
id: TASK-209
title: Dash redesign · B.5 — Review consequence (the closed loop)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-208
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 199000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every verdict produces a clear, honest consequence screen so the operator sees the loop close. Parent: EPIC B (task-199). Covers AC §15.

OWNERSHIP: this is the display layer only. It renders the honest consequence of a verdict and the exact-transition line, and routes the verdict (verb + comment) emitted by the brief reader (TASK-208). The actual state mutation — criterion → met, goal-rollup move, run resume — is owned by the live-state model (TASK-216) + the coordinator. This screen reflects what 216 did; it must never perform the mutation itself.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S15.1 on Approve/Answers at a spawned-run checkpoint, the screen states the run continues — resumes remaining steps, ends at the stopping brief, returns only at another checkpoint (the resume is performed by TASK-216, not here)
- [ ] #2 S15.2 on Approve/Answers for a criterion-linked brief, the screen shows the exact transition in a monospace line (e.g. "criterion · waiting-on-you → met") and that the goal rollup moved + the run resumed — reflecting the mutation TASK-216 performed; this screen does not write it
- [ ] #3 S15.3 Request revisions: the screen states the run revises and returns to the inbox as a new version; nothing is marked met
- [ ] #4 S15.4 Reject: screen states the run drops the direction, criterion unchanged. Ignore: set aside, criterion unchanged
- [ ] #5 S15.5 when a criterion advanced, offer See-the-goal alongside Back-to-origin; the consequence copy must accurately match the verdict and whether a run resumes
- [ ] #6 the verdict the operator submits is emitted once (by TASK-208) and the resulting transition is read back from the bus/live-state; the screen holds no authoritative copy of run or criterion state
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

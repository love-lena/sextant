---
id: TASK-210
title: Dash redesign · B.6 — Link a workstream to a criterion
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-220
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: low
ordinal: 200000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Attaching existing work to a goal criterion (many-to-many). Parent: EPIC B (task-199). Covers AC §14.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S14.1 two-column flow: target criterion left, candidate existing runs/workflows right; linking is many-to-many (one workstream can feed several criteria)
- [ ] #2 S14.2 candidate rows toggle +link <-> ✓linked on click; a build-a-workflow-for-this affordance opens the builder
- [ ] #3 Persistence/proof: linking writes the relation durably on the artifact side (relates); after reload the criterion shows its linked workstream(s) re-derived from the bus
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

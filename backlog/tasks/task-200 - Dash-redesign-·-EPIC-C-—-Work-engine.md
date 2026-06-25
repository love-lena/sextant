---
id: TASK-200
title: Dash redesign · EPIC C — Work engine
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - epic
  - lane-work-engine
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 190000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Where work is defined as a spec and watched as it runs: Work engine list, Spawn work, Workflow builder, Template detail, Run view, and the live state model that makes runs walk their steps. The DATA STEP is the run-record contract (ADR-0048, TASK-193) which gates the run/template surfaces. Post-pivot CUT: §11 take-the-keyboard is removed from Run view. Children: TASK-193 (run-record contract / data step), C.1 Work engine list, C.2 Spawn work, C.3 Builder, C.4 Template detail, C.5 Run view, C.6 Live state model.

Carries AC sections 6, 7, 8, 9, 10, 21.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Run-record contract (ADR-0048) landed; all Work engine child slices merged
- [ ] #2 A workflow is the reusable de-named template (WORKFLOW.md); a run is one live instance, ULID-identified, never a persona
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

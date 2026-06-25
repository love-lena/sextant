---
id: TASK-211
title: Dash redesign · C.1 — Work engine list (Workflows + Active runs)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-work-engine
dependencies:
  - TASK-193
  - TASK-220
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 201000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Work engine landing surface. Parent: EPIC C (task-200). Covers AC §6.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S6.1 header with a Spawn work action and two sections: Workflows and Active runs
- [ ] #2 S6.2 Workflows (subtitled WORKFLOW.md) lists reusable specs: name, live-run count badge when >0, edit-spec affordance, step recipe, primary trigger tag; empty state prompts to describe one
- [ ] #3 S6.3 a workflow row opens its template detail (§9 / C.4); edit-spec opens the builder (§8 / C.3); a describe-a-new-workflow affordance opens the builder fresh
- [ ] #4 S6.4 Active runs lists every in-progress/waiting/blocked run with live pulse in status color, label, ULID, via-{workflow} or ad-hoc, goal (or no goal yet); rows open the run; empty state explains
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

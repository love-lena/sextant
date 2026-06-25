---
id: TASK-207
title: Dash redesign · B.3 — Criteria proposal
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-206
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 197000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
After a charter is written, a workflow proposes acceptance criteria the operator curates before the goal goes live. Parent: EPIC B (task-199). Covers AC §17.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S17.1 shows the north star + proposed criteria derived from the charter's how-we'll-know-it's-done section
- [ ] #2 S17.2 each proposal can be accepted (toggle), edited inline, or dropped; operator can add their own
- [ ] #3 S17.3 footer count of proposed; Accept all -> goal live creates the goal with accepted criteria and opens it; only then can workflows attach
- [ ] #4 Persistence/proof: Accept all -> goal live creates a durable goal artifact (with the accepted criteria) on the bus; after reload the goal appears in the portfolio re-read from the bus — not local state
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

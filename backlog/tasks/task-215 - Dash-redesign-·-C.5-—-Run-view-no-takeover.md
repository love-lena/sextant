---
id: TASK-215
title: Dash redesign · C.5 — Run view (no takeover)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-work-engine
dependencies:
  - TASK-212
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 205000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Watching one live run; watching != being needed. Post-pivot CUT: §11 take-the-keyboard is NOT built — steering is by posting to the run topic only. Parent: EPIC C (task-200). Covers AC §10 minus §11.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S10.1 top bar shows the run ULID and its goal (or no goal yet); body leads with run label + one-line description. (NO take-the-keyboard action — §11 cut)
- [ ] #2 S10.2 Workflow steps timeline: done steps met, current step in-progress (or needs-you + operator-checkpoint tag if waiting), upcoming dimmed; terminal step labelled write-the-stopping-brief
- [ ] #3 S10.3 Run timeline: chronological activity log, each entry with status glyph, text, source ULID, time; a ...working... pending line while in-progress
- [ ] #4 S10.4 Draft artifacts: artifacts the run produced (name, kind, version, status chip); rows open the brief when one exists
- [ ] #5 S10.5 Run topic: a posting composer to steer the run without taking over; posts append to a visible thread attributed to you
- [ ] #6 Persistence/proof: a run-topic post publishes a durable message; after reload it appears in the run thread re-read from the bus — not local state
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

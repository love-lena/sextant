---
id: TASK-218
title: Dash redesign · D.2 — Goal detail
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-home-goals
dependencies:
  - TASK-217
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 208000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The goal as a working-backwards document with live criteria and attached work. Parent: EPIC D (task-201). Covers AC §5.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S5.1 top bar shows stream tag, M-of-N met, a +Spawn work action; body leads with the north star + a Done-when-every-criterion-below-is-true line
- [ ] #2 S5.2 each criterion row shows a status icon/color, the claim text, the run(s) working toward it (ULID chips), and a status label
- [ ] #3 S5.3 a criterion routes by status: waiting -> its brief; in-progress -> watch the run; blocked -> see the blocker; not-started -> no work yet
- [ ] #4 S5.4 per-criterion inline actions: blocked offers +link a workstream (§14 / B.6); waiting/not-started offer +spawn work (§7); an in-progress/blocked run chip offers watch
- [ ] #5 S5.5 Add criterion: +Add criterion reveals an inline input; Enter adds a not-started criterion (persists for the session, visible to Spawn work), Esc cancels
- [ ] #6 S5.6 Goal topic: a posting composer to post a message to the goal's topic; posts append to a visible thread attributed to you - just now
- [ ] #7 Persistence/proof: a goal-topic post publishes a durable message and Add criterion updates the goal artifact; after reload both the thread post and the new criterion are re-derived from the bus
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

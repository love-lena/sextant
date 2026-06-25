---
id: TASK-202
title: Dash redesign · 0.2 — Command palette (⌘K)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-foundation
dependencies:
  - TASK-220
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 192000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Keyboard-driven jump-to-anything overlay that also starts actions. Parent: EPIC 0 (task-197). Covers AC §2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S2.1 ⌘K / Ctrl+K anywhere toggles the palette; the sidebar search button also opens it
- [ ] #2 S2.2 indexes type-tagged rows: Actions (New doc/New workflow/New goal), Goals, Workflows, Runs, Artifacts, Surfaces
- [ ] #3 S2.3 typing filters by keyword (label, subtitle, metadata); results capped (~9)
- [ ] #4 S2.4 keyboard nav: up/down move selection, Enter activates, Esc closes; hover selects
- [ ] #5 S2.5 activating navigates / runs the action and closes; clicking the scrim closes without acting
- [ ] #6 S2.6 no-match reads No matches for '...'; footer shows up/down / enter / esc hints
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

---
id: TASK-204
title: Dash redesign · 0.4 — Status system + Tweaks / data-mode
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
priority: medium
ordinal: 194000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The shared status vocabulary used everywhere, plus the Tweaks panel data-state toggle. Parent: EPIC 0 (task-197). Covers AC §0 (status vocab), §22.3, §1.9, §21.1.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Five canonical statuses with one color + one glyph each: Met ✓ green #3f8f59, In progress ◐ blue #3a82c4, Waiting-on-you ● terracotta #c0573b, Blocked ⊘ amber #b9842a, Not started ○ grey #9a9ea7
- [ ] #2 S22.3 the same color+glyph apply on criteria, runs, drafts, timelines, chips — every surface
- [ ] #3 S1.9 / S21.1 Tweaks panel toggles data state between Snapshot (seeded demo) and Blank slate (empty), with Reset to empty in blank mode; choice persists (sextant.synth.datamode.v1)
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

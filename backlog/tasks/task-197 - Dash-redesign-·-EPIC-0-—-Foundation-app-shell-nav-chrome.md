---
id: TASK-197
title: 'Dash redesign · EPIC 0 — Foundation: app shell, nav & chrome'
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - epic
  - lane-foundation
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 187000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The shell every other lane renders inside. Adopt the design's shell, nav, command palette, floating assistant, status system, and cross-cutting conventions EXACTLY (post-pivot: no personas, no takeover, no quick-decision, [[wikilinks]] in place of reference chips). Build in stages per surface: filler page -> functionality -> full feature. Children: TASK-220 (shell+nav+filler), TASK-194 (no-personas sweep), plus the palette / assistant / status-system slices below.

Carries AC sections 0, 1, 2, 20, 22. Per-criterion claims live on the child slices; this epic is the lane's AC home and tracking parent.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 All Foundation child slices merged; every nav destination reachable
- [ ] #2 S0.6 visual system holds: white surfaces, hairline borders, cool greys, charcoal #15171c sidebar, Hanken Grotesk UI + IBM Plex Mono labels/ULIDs; no parchment
- [ ] #3 S22.3 status consistency: the five statuses use one color + one glyph each on every surface
- [ ] #4 S22.4 calm by default: live updates appear without focus-pulling animation; handled/finished work hidden or collapsed
- [ ] #5 S22.6 empty states everywhere: every list/surface has a calm instructive empty state with a next action
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

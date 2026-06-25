---
id: TASK-220
title: >-
  Dash redesign: adopt the design shell, nav & chrome (filler pages for new
  sections)
status: Done
assignee: []
created_date: '2026-06-24 00:33'
updated_date: '2026-06-25 02:31'
labels:
  - ready-for-agent
  - lane-foundation
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant.html
priority: high
ordinal: 182000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Adopt the Sextant design's app shell end-to-end. The left nav becomes Home · Goals · Work engine · Artifacts · Bus (the design's exact order + charcoal chrome); the floating Assistant button and the ⌘K palette chrome are present on every surface; and the new sections (Work engine, Bus) render as inert filler pages so the shell is complete and navigable before their features exist.

Build/iterate with `sextant-dash --port 0 --ui <worktree>/clients/go/apps/internal/dashapi/web/app` — serves the SPA from disk, no Go rebuild for UI-only changes, side-by-side with the prod dash on :8765 (ADR-0046). The browser is a direct NATS-WS co-equal client (ADR-0044). The design prototype is the visual reference (iterated with the operator); the concrete criteria below are the gating bar.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S1.1 the sidebar shows the Sextant brand mark (logomark glyph + wordmark) at top, in teal/purple on charcoal.
- [ ] #2 S1.2 the left nav lists exactly five rows in order: Home, Goals, Work engine, Artifacts, Bus; the active row is visually marked.
- [ ] #3 S1.3 clicking a nav row navigates the stage to that surface and resets that root's back-stack; existing surfaces unregressed.
- [ ] #4 S1.6/1.7 detail/overlay surfaces render a top bar with a back button labelled by the originating surface; back returns to the exact origin (a goal opened from Home returns to Home; from Goals returns to Goals).
- [ ] #5 S0.6 visual system: white main surfaces, hairline borders, cool neutral greys, a charcoal #15171c sidebar; Hanken Grotesk for UI, IBM Plex Mono for labels/meta/ULIDs; no parchment/warm paper.
- [ ] #6 S1.5/S22.2 sidebar collapses to an icon rail and expands; collapsed state persists across reloads under localStorage key `sextant.sidebar.collapsed.v1`; operator-owned keys are never overwritten.
- [ ] #7 S1.4/S1.8 a Search button above the nav labelled "Search…" with a ⌘K hint is present, and a floating Assistant spark button is present on every surface (chrome only — palette behavior in TASK-202, assistant behavior in TASK-203).
- [ ] #8 Work engine and Bus render as clearly-placeholder filler pages — navigable, no errors.
- [ ] #9 Visual fidelity to the design prototype is a non-gating reference check, iterated with the operator; the concrete criteria above are the gating bar.
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

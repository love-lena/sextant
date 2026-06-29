---
id: TASK-301
title: Home is just a shortcut to an HTML artifact
status: To Do
assignee: []
created_date: '2026-06-29 23:20'
labels:
  - feature
  - dash
  - home
  - ui
  - simplification
  - 'slug:feat-dash-home-html-artifact-shortcut'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 230000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Today the dash Home is a bespoke surface: dedicated components (GoalsBox, agenda / next-action, needs-you inbox) backed by the `home` artifact in a structured projection shape (dash-redesign EPIC D). Now that HTML artifacts render sanitized in the dash reader (TASK-222, ADR-0050), Home could collapse to: the `home` artifact is just an HTML `document`, and 'Home' in the nav is a shortcut that opens that artifact in the generic artifact reader. The assistant (violet) authors/curates the HTML directly. Net effect: drop the special-cased Home page + its components in favour of the one HTML renderer; Home becomes as expressive as any artifact and fully author-controlled, instead of a fixed box layout. Fits the deep-modules / less-bespoke-UI direction.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The dash nav 'Home' entry opens the `home` artifact rendered by the generic (sanitized) HTML-artifact reader — no Home-specific React surface in the render path
- [ ] #2 violet writes the `home` artifact as an HTML `document` ({$type:document, format:html}); whatever it authors is what Home shows
- [ ] #3 The bespoke Home components (GoalsBox / agenda / needs-you boxes) are removed OR explicitly retained as a documented decision — the ticket is closed only once that fork is resolved, not left half-migrated
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Decide the fork first (see open question). If pure-rendering: point the Home route at the artifact reader keyed to name=home, have violet emit format:html, delete the Home page components. If keeping structure: violet computes the projection and emits it AS html, dash still just renders the artifact.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Provenance: operator idea, 2026-06-29. Open product call (why ready-for-human): do we give up the structured Home (typed needs-you/review projection + derived next-action) for freeform HTML, or keep the projection logic (violet computes it) and only collapse the *rendering* to HTML? i.e. rendering-only simplification vs also dropping the structured home lexicon. Related: [[feat-dash-render-html-artifacts]] (TASK-222), [[feat-native-html-artifacts]] (TASK-133), ADR-0050, dash-redesign EPIC D (TASK-201), [[feat-home-one-next-action]] (TASK-120), [[feat-violet-curates-home]] (TASK-144).
<!-- SECTION:NOTES:END -->

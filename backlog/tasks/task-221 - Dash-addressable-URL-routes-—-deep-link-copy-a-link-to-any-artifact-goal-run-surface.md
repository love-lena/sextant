---
id: TASK-221
title: >-
  Dash: addressable URL routes — deep-link + copy a link to any artifact / goal
  / run / surface
status: To Do
assignee: []
created_date: '2026-06-25 00:45'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-foundation
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant.html
priority: medium
ordinal: 210000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Today the dash SPA navigates by in-memory nav state — the only thing in the URL is the access token, so no surface or record is addressable and the operator can't copy a link to a specific artifact, goal, run, or brief. Add URL-path routing so every surface and detail/overlay has a stable, shareable path that deep-links on load and updates as you navigate (browser back/forward map to the nav back-stack), without breaking the token-in-URL auth. This is the deeper half of 'links aren't always working': there are no addressable paths to link to. Browser-direct client per ADR-0044; preserve the back-to-origin behavior (S1.7) alongside real URL history.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every primary surface (Home, Goals, Work engine, Artifacts, Bus) and every detail/overlay (goal detail, run view, brief reader, spawn, builder, template, composer, link, criteria) has a stable URL path — e.g. /goals/<id>, /artifacts/<name>, /runs/<ulid>, /work-engine, /bus
- [ ] #2 Navigating updates the URL via history pushState; browser Back/Forward map to the nav back-stack and restore the exact prior surface (S1.7 back-to-origin preserved)
- [ ] #3 Loading a deep-link URL directly opens that surface/record, resolving the record from the bus; an unknown/missing record shows a graceful not-found state, not a blank/error
- [ ] #4 A visible 'copy link' affordance (and the address bar) yields a URL that reopens the same view for the operator
- [ ] #5 Auth is not broken: the access token is session/loopback-scoped — document that a copied link works within the operator's own session; do not require leaking the token into a shared link, and do not regress token handling
- [ ] #6 No console errors; existing in-app navigation, the command palette (deep-links via routes), and [[wikilinks]] all route through the same addressable paths
<!-- AC:END -->

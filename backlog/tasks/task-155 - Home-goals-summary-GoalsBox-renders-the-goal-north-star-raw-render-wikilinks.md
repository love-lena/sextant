---
id: TASK-155
title: >-
  Home goals-summary (GoalsBox) renders the goal north-star raw — render
  wikilinks
status: To Do
assignee: []
created_date: '2026-06-17 20:43'
labels:
  - feature
  - dash
  - goals
  - wikilinks
  - ergonomics
  - 'slug:feat-dash-home-goalsbox-wikilinks'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 145000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
On the Home page, the 'Goals · N need you' summary box (home.jsx GoalsBox) renders each goal's north-star (g.northstar) as plain text, so [[wikilinks]] show as literal brackets. The dedicated Goals page (goals.jsx, fixed in #181) + artifact bodies + the Home agenda all render wikilinks; the GoalsBox is the one remaining spot. Found while fixing Lena's 'wikilinks don't work in the goals page' #ui-feedback — she meant the Goals view (fixed); this Home summary is the related straggler.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 GoalsBox renders [[wikilinks]] in the goal north-star: known target → clickable (sx-artlink), unknown → muted (sx-artlink-dead), matching goals.jsx/home-agenda
- [ ] #2 The goalsum-row (currently a <button>) is made a div[role=button] (or the wikilink rendered outside it) so the interactive wikilink spans aren't nested inside an interactive button (same a11y fix as the agenda card + goals card)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Thread the shared renderWiki (app.jsx) into home.jsx's GoalsBox (it already reaches HomePage via ctx); apply to g.northstar; convert the fx-goalsum-row button → div[role=button] + onKeyDown.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #181 (goals-page wikilinks). v0.5.1 fast-follow. Related: [[feat-dash-goals-ui]], the #169 artifact-body wikilink renderer, the #178 agenda wikilinks.
<!-- SECTION:NOTES:END -->

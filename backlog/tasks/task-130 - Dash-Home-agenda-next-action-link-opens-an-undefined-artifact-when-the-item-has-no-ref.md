---
id: TASK-130
title: >-
  Dash: Home agenda 'next action' link opens an undefined artifact when the item
  has no ref
status: To Do
assignee: []
created_date: '2026-06-16 20:23'
labels:
  - bug
  - dash
  - home
  - 'slug:bug-dash-home-agenda-link-undefined'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 120000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Home 'Needs you' agenda renders each item's link as ctx.onOpenArtifact(it.ref) (internal/dashapi/web/app/home.jsx). When a curated-home agenda item omits 'ref' (or it's stale/unresolvable), the click opens an UNDEFINED artifact — a dead next-action. This directly undercuts the TASK-120 tenet 'Home always shows one next action': a broken action is worse than none. Hit live during the v0.5 kickoff — the operator's one Home action (open the goals grill) was a dead link because the curated 'home' record's agenda item had no 'ref'. Worked around by setting ref on the record, but the dash must be robust: a refless/unresolvable agenda item must never render a clickable dead link.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An agenda item with no 'ref' does not render as a clickable link — it is inert (or a clear disabled state), never navigates to an undefined artifact
- [ ] #2 When 'ref' names an artifact that does not exist or is not yet loaded, the click is a graceful no-op (or a 'not found' notice), not a navigation to undefined
- [ ] #3 The v0.5 redesign's Home 'Start here' action always resolves to a real, openable target (don't reintroduce the dead-link class)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: v0.5 dash redesign kickoff (2026-06-16) — blocked the operator from reaching the goals grill. Number claimed via backlog.counter CAS (130). Related: TASK-120 (the one-next-action tenet); the v0.5 redesign (artifacts v0-5-charter / v0-5-dash-design — its Home replaces this surface). Sibling finding, NOT filed separately (redesign-scope, not a current-dash code bug): the Artifacts list doesn't surface needs-you items — cluttered with settled briefs all at 'review' status; the redesign's Artifacts view ('N awaiting you') addresses it.
<!-- SECTION:NOTES:END -->

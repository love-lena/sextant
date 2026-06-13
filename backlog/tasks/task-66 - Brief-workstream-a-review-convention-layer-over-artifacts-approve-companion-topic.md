---
id: TASK-66
title: >-
  Brief workstream: a review/convention layer over artifacts (approve +
  companion topic)
status: To Do
assignee: []
created_date: '2026-06-12 19:43'
updated_date: '2026-06-13 03:24'
labels:
  - feature
  - artifacts
  - ux
  - 'slug:feat-brief-workstream-convention'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 72000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The per-artifact feedback topic plus mark-approved affordances are conventions layered on the core artifact primitive, not changes to the core artifact convention (primitives-not-policy). lena named the layer 'brief workstream' (working name). Surfaced 2026-06-12 by repeatedly hand-creating a feedback topic per artifact (frontend-dash, orchestration-m5, principal-hardening-summary) during planning, and wanting an easy approve-this-artifact action.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An artifact can carry an approved review-state by convention (over the record/labels), not baked into the core primitive
- [x] #2 An artifact gets a default companion discussion topic by convention (same id maps to a msg subject) so commenting needs no manual topic setup
- [x] #3 The dash/UI exposes an easy mark-approved affordance
- [x] #4 The core artifact operations (create/get/update/list/watch) and protocol/methods.json are unchanged
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Define a brief-workstream convention over artifacts: a review-state plus a derived companion-topic naming rule plus UI affordances; keep it out of the core artifact primitive.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Provenance: 2026-06-12 planning, lena on msg.topic.helm. Naming and scope are lena's design call (ready-for-human).

Dash-side implemented as a convention in D2 (TASK-71, ADR-0033): review-state as a review block in the artifact record (POST /api/artifacts/{name}/review, read-merge-CAS); companion topic msg.topic.artifact.<name>; Approve/Request-changes/Archive/Reject + Reopen affordances; core artifact ops unchanged. All 4 ACs met on the dash side. A CLI affordance (e.g. sextant artifact review) is the remaining optional piece. Status/closure is lena's call (ready-for-human).

2026-06-12: Convention: an artifact's author should subscribe to msg.topic.artifact.<name> so they receive discussion/review events on their own artifact.
<!-- SECTION:NOTES:END -->

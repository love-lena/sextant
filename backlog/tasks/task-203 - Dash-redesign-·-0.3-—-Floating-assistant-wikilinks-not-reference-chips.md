---
id: TASK-203
title: 'Dash redesign · 0.3 — Floating assistant (wikilinks, not reference chips)'
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-foundation
dependencies:
  - TASK-192
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 193000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Universal floating helper for quick questions about the state of things, distinct from work agents. Post-pivot: S20.3 reference chips are REPLACED by [[wikilinks]] for flexibility. Parent: EPIC 0 (task-197). Covers AC §20 (S20.3 adapted).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S20.1 a floating spark button opens a chat panel (Assistant - always here) on every surface; an x closes it
- [ ] #2 S20.2 answers questions about goals, what's waiting, where workstreams stand; with no data says so and points to defining a goal
- [ ] #3 S20.3 adapted: answers may embed [[wikilinks]] that resolve to the named goal/run/artifact/surface and navigate on click (NOT the old reference-chip widget)
<!-- AC:END -->

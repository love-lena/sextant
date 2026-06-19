---
id: TASK-131
title: Move backlog onto the bus
status: To Do
assignee: []
created_date: '2026-06-16 20:36'
labels:
  - feature
  - backlog
  - bus
  - design
  - 'slug:feat-backlog-on-the-bus'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 121000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Backlog (the Backlog.md file-based ticket tracker under backlog/) is how WE develop sextant, but it's a separate file-based tool that does not ship with sextant and isn't a bus citizen. Lena's direction (2026-06-16, during the v0.5 goals grill): move backlog onto the bus — sextant tracking its own development as bus-native artifacts, dogfooding the primitives. This also cleanly separates 'our dev tooling' from 'the product': sextant applies to other work without backlog, and goals/criteria reference evidence artifacts (never tickets) per the v0.5 goals model. A ticket becomes an artifact (or a goal/criterion) on the bus; the board/triage/status become projections + curation, same as the v0.5 Home.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A sextant dev ticket lives as a bus artifact (record shape decided in design), not only as a backlog/ file
- [ ] #2 The board / list / triage views are projections over bus state (consistent with the v0.5 inbox-as-projection model)
- [ ] #3 Migration path for the existing backlog/ tickets is defined (or an explicit decision to start fresh)
- [ ] #4 Cross-link, don't couple: product artifacts MAY reference a ticket but never depend on the backlog tool
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: v0.5 goals-design grill (2026-06-16, Decision 8 — goals are generic, must not depend on backlog tickets). Number claimed via backlog.counter CAS (131). Design call (how a ticket maps onto bus primitives — artifact vs goal/criterion; how board/triage project) → ready-for-human. Related: the v0.5 goals model (artifact goals-design), v0-5-charter; dogfoods signal-not-manage + projection-as-view.
<!-- SECTION:NOTES:END -->

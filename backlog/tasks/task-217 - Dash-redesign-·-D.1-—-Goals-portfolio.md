---
id: TASK-217
title: Dash redesign · D.1 — Goals portfolio
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-home-goals
dependencies:
  - TASK-220
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 207000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
All goals as cards, ranked into three attention buckets. Parent: EPIC D (task-201). Covers AC §4.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S4.1 goals bucketed into Needs you, Not started, Moving on its own, each a labelled header with a count; empty buckets omitted
- [ ] #2 S4.2 Needs you = goals with a waiting or blocked criterion; Not started = no work running and nothing met; Moving on its own = settled or genuinely in flight
- [ ] #3 S4.3 an inline New goal creator at top: a one-line north-star input; Write the charter opens the Composer pre-seeded with that north star (§16)
- [ ] #4 S4.4 a goal card shows name, stream tag, escape-hatch tag when applicable, rollup verdict chip, north star, and an activity map (one colored segment per criterion) + M-of-N met summary
- [ ] #5 S4.5 Moving-on-its-own cards list active run chips (live pulse + label + watch) that open the run without navigating into the goal
- [ ] #6 S4.6 Not-started cards show a dashed No-work-running-yet / +spawn-work chip that opens Spawn work for that goal
- [ ] #7 S4.7 clicking a card body opens goal detail (§5 / D.2); run + spawn chips stop propagation
<!-- AC:END -->

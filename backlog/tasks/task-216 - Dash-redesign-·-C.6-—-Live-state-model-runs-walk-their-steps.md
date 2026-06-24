---
id: TASK-216
title: Dash redesign · C.6 — Live state model (runs walk their steps)
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-work-engine
dependencies:
  - TASK-215
  - TASK-208
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 206000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The behavior that makes it real: runs genuinely walk steps, checkpoints raise briefs, clearing a brief resumes the run. Honors ADR-0048 stop conditions (terminal stopping brief; checkpoint pauses). Parent: EPIC C (task-200). Covers AC §21.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S21.2 a spawned run walks its steps automatically on a timer: work steps complete, append activity, advance
- [ ] #2 S21.3 at an operator-checkpoint step it pauses, sets itself waiting-on-you, raises a brief (document or quick-decision per step form — note: quick-decision form cut, document only), brief appears in Home + the goal's criterion
- [ ] #3 S21.4 clearing the checkpoint (Approve/Answers) resumes the run past the checkpoint to in-progress until the next checkpoint or terminal step
- [ ] #4 S21.5 the terminal step writes the stopping brief, sets the run met, advances its criterion (via relates:toward) to met, adds the run to finished-while-you-were-away
- [ ] #5 S21.6 spawning toward a not-started/blocked criterion moves it to in-progress and (for template runs) records the run in the template's run history
- [ ] #6 S21.7 all surfaces re-render reactively on store mutation (mode switch, create goal, spawn run, step tick); no manual refresh
<!-- AC:END -->

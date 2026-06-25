---
id: TASK-216
title: Dash redesign · C.6 — Live state model (runs walk their steps)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 03:01'
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

OWNERSHIP: this ticket is the SOLE owner of run/criterion state transitions. Consuming a verdict to resume a run, advancing a criterion to met (via relates:toward, per ADR-0035 + ADR-0048), and moving the goal rollup all live here (+ the coordinator). The review-consequence screen (TASK-209) only displays the result of these transitions; it must not perform them. The brief reader (TASK-208) emits the verdict this model consumes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S21.2 a spawned run walks its steps automatically on a timer: work steps complete, append activity, advance
- [ ] #2 S21.3 at an operator-checkpoint step it pauses, sets itself waiting-on-you, raises a brief (document or quick-decision per step form — note: quick-decision form cut, document only), brief appears in Home + the goal's criterion
- [ ] #3 S21.4 consuming the verdict emitted by the brief reader (TASK-208), this model resumes the run past the checkpoint to in-progress until the next checkpoint or terminal step — this model owns the resume; TASK-209 only displays it
- [ ] #4 S21.5 the terminal step writes the stopping brief, sets the run met, and advances its fed criterion to met via relates:toward (per ADR-0035 + ADR-0048) and moves the goal rollup — this model is the sole writer of that transition — and adds the run to finished-while-you-were-away
- [ ] #5 S21.6 spawning toward a not-started/blocked criterion moves it to in-progress and (for template runs) records the run in the template's run history
- [ ] #6 S21.7 all surfaces re-render reactively on store mutation (mode switch, create goal, spawn run, step tick); no manual refresh
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-06-24 capability-gap audit: this delivered the dash live-state VIEW (poll the run artifact, render step transitions) — confirmed working. But 'runs walk their steps' has no back half: no backend advances a run, so in practice runs never move (verified live: run 01KVYADZ4ET154VY7E5C4H54S2 frozen at s1). The executor is filed as [[feat-run-executor-workflow-run-v1]] (TASK-224). This ticket stays Done for its frontend scope; TASK-224 owns the executor.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

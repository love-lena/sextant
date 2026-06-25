---
id: TASK-212
title: Dash redesign · C.2 — Spawn work (three-step)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-work-engine
dependencies:
  - TASK-193
  - TASK-211
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 202000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Instantiating a run: the workflow defines the how, spawning gives it a concrete what pointed at a goal. Binds goal/criterion via the run-record contract (ADR-0048, relates kind:toward). Parent: EPIC C (task-200). Covers AC §7.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S7.1 three-step form: 1-How (pick a workflow template), 2-On (task objective free text), 3-Toward (a goal + the criterion it works toward)
- [ ] #2 S7.2 step 1 offers the always-available base template (Investigate -> review -> brief) + every existing workflow + a +new-template that opens the builder; selecting shows its step line
- [ ] #3 S7.3 step 3 lets the operator pick any goal or No-goal-yet; picking a goal reveals its criteria as selectable chips
- [ ] #4 S7.4 a live summary previews what will spawn (Spawns {workflow} on '{task}' -> {goal} - toward {criterion}) plus the new run ULID and whether it runs to completion or pauses at a checkpoint
- [ ] #5 S7.5 Spawn & watch is disabled until a task objective is entered; spawning creates the run, navigates to its run view, and the run begins walking its steps
- [ ] #6 Per ADR-0048: the run is sextant.workflow.run/v1 (ULID, ad-hoc => template:null); the goal binding is written as relates:[{goal,crit,kind:"toward"}] on the run, not a bespoke field
- [ ] #7 Persistence/proof: spawning publishes a sextant.workflow.run/v1 artifact (ULID, template-or-null, relates:[{goal,crit,kind:toward}]) to the bus; after a reload the run appears in Active runs and the run view, re-derived from the bus — not local state
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->

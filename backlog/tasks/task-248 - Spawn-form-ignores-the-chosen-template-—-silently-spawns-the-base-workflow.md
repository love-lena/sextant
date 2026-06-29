---
id: TASK-248
title: Spawn form ignores the chosen template — silently spawns the base workflow
status: To Do
assignee: []
created_date: '2026-06-29 18:28'
labels:
  - bug
  - dash
  - work-engine
  - P2
  - needs-triage
  - 'slug:bug-spawn-form-drops-chosen-template'
dependencies: []
priority: medium
ordinal: 230000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The work-engine Spawn form always defaults to the BASE template and drops a template chosen elsewhere, so a run can spawn the base workflow when the operator meant a specific one. In workengine.jsx, SpawnWork initializes tplName to templates[0] (the base, 'Investigate -> review -> brief') and does NOT receive view.payload — the template passed by TemplateDetail's onSpawn (setView({name:'spawn', payload:tpl})). So opening a workflow and clicking Spawn lands on a form pre-set to BASE; unless the operator re-clicks the template tile in step 1, handleSpawn resolves tpl=base and writes template:null with the base steps. Evidence: run 01KWA9ZJEB47QM3J2HX6Z4DBX0 — operator expected a template's steps, got the base three (Investigate/Pause/Brief). Confusing + silently wrong.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Spawning from a template (TemplateDetail Spawn action) pre-selects THAT template in the Spawn form: SpawnWork initializes tplName from the passed template (view.payload), shown is-on in step 1.
- [ ] #2 The spawned run's steps AND template field reflect the chosen template (a non-base template -> template:<name> + its steps), not the base. Verified: open template X -> Spawn -> spawn -> run record has template==X and X's steps.
- [ ] #3 The active workflow selection is unmistakable in the form (the operator can never spawn base by inattention when they navigated from another template).
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Pass view.payload into SpawnWork (the spawn view-switch drops it today) and initialize tplName from it: useState(initial?.name || templates[0]?.name). Optionally require an explicit pick / make the default visually loud.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered 2026-06-29 diagnosing run 01KWA9ZJEB47QM3J2HX6Z4DBX0 on the rc. Relates to the work engine ([[feat-run-executor-workflow-run-v1]] TASK-236) + the spawn UI (TASK-212).
<!-- SECTION:NOTES:END -->

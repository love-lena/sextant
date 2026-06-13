---
id: TASK-26
title: 'Reference Workflow coordinator: sextant.workflow/v1'
status: To Do
assignee: []
created_date: '2026-06-04 18:05'
updated_date: '2026-06-12 21:15'
labels:
  - 'slug:feat-m5-workflow-coordinator'
milestone: 'M5: Orchestration (spawn + workflows)'
dependencies: []
references:
  - docs/adr/0011-workflows.md
priority: medium
ordinal: 25000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
M5.4 of the approved M5 breakdown (artifact orchestration-m5-effort). The coordinator is an ordinary client that runs the workflow engine as a library (no engine in core). Layer-0 contract: state in sx_workflows keyed by id, plus sx.workflow.<id>.{control,events} subjects. Layer-1 sextant.workflow/v1: status, owner, granular steps[], control vocab (cancel/pause/resume/approve), checkpoint-to-artifact resume. COMPOSES the rest: a workflow step can dispatch an agent (M5.2, TASK-25) or invoke a registered function (M5.3, sextant run). Pure bus primitives (bucket + subjects already exist on main) -- a parallel client-side module; the step-runner + idempotent resume is the net-new part. The dash workflow cards (TASK-7) render this.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Layer-0 contract implemented (state envelope in sx_workflows + control/events subjects)
- [ ] #2 Coordinator walks the steps, checkpoints state to an Artifact (CAS), emits events, accepts cooperative control
- [ ] #3 sextant.workflow/v1 record shape (status, owner, steps[]) defined and versioned in its kind tag
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Aligned to the approved M5 breakdown (artifact orchestration-m5-effort) by canopus 2026-06-12. M5.4. Composes [[feat-m5-client-standup]] (M5.2, TASK-25) + [[feat-m5-sextant-run]] (M5.3). Refs ADR-0011.
<!-- SECTION:NOTES:END -->

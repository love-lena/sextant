---
id: TASK-26
title: 'Reference Workflow coordinator: sextant.workflow/v1'
status: To Do
assignee: []
created_date: '2026-06-04 18:05'
labels: []
milestone: 'M4: Orchestration (spawn + workflows)'
dependencies: []
references:
  - docs/adr/0011-workflows.md
priority: medium
ordinal: 25000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Deferred out of the MVP (agents can communicate and do the work unmanaged; formal workflows just make it more managed). The coordinator is an ordinary client that runs the workflow engine as a library (no engine in core). Layer-0 contract: state in sx_workflows keyed by id + sx.workflow.<id>.{control,events} subjects. Layer-1 sextant.workflow/v1: status, owner, granular steps[], control vocab (cancel/pause/resume/approve), checkpoint-to-artifact resume. The dash's workflow cards (TASK-7) render this.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Layer-0 contract implemented (state envelope in sx_workflows + control/events subjects)
- [ ] #2 Coordinator walks the steps, checkpoints state to an Artifact (CAS), emits events, accepts cooperative control
- [ ] #3 sextant.workflow/v1 record shape (status, owner, steps[]) defined and versioned in its kind tag
<!-- AC:END -->

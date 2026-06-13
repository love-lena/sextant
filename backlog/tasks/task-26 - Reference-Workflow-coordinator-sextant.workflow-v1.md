---
id: TASK-26
title: 'Reference Workflow coordinator: sextant.workflow/v1'
status: In Progress
assignee: []
created_date: '2026-06-04 18:05'
updated_date: '2026-06-13 02:59'
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
- [x] #1 Layer-0 contract implemented (state envelope in sx_workflows + control/events subjects)
- [x] #2 Coordinator walks the steps, checkpoints state to an Artifact (CAS), emits events, accepts cooperative control
- [x] #3 sextant.workflow/v1 record shape (status, owner, steps[]) defined and versioned in its kind tag
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Aligned to the approved M5 breakdown (artifact orchestration-m5-effort) by canopus 2026-06-12. M5.4. Composes [[feat-m5-client-standup]] (M5.2, TASK-25) + [[feat-m5-sextant-run]] (M5.3). Refs ADR-0011.

M5.4 built on branch feat/m5-workflow: cmd/sextant-workflow (engine-as-library coordinator) drives a declarative workflow — state in a sextant.workflow/v1 Artifact (CAS-checkpointed), events on msg.workflow.<id>.events, cooperative control on msg.workflow.<id>.control — and a 'dispatch' step COMPOSES the M5.2 dispatcher (spawn.request → correlate spawn.ack → await the agent's step-done). Idempotent step-granular resume. Self-validating docs/demos/m5-workflow-demo.sh = 8/8 (token-free; composes M5.2 end-to-end). FINDING: the reserved sx.workflow.*/sx_workflows Layer-0 names aren't client-reachable (sx namespace is bus-only, ADR-0012), so the coordinator realizes Layer-0 over msg.* + ARTIFACTS per ADR-0011's convention-over-primitives — no core change. M5.3 request/reply helper NOT needed (spawn.ack pattern covers it; TASK-23 stays parked).
<!-- SECTION:NOTES:END -->

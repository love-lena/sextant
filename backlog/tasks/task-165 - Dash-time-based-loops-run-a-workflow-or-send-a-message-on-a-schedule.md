---
id: TASK-165
title: 'Dash: time-based loops — run a workflow or send a message on a schedule'
status: To Do
assignee: []
created_date: '2026-06-18 01:17'
labels:
  - feature
  - dash
  - loops
  - scheduling
  - violet
  - workflow
  - 'slug:feat-dash-loops'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 155000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A LOOP = a time-based trigger that, on each tick (e.g. every 10 min), either runs a workflow OR sends a message. From Lena's outbox 2026-06-17: 'in addition to workflows in the dash we also need loops... run a workflow or send a message on a time-based loop. This is how i might add an extra check for violet to check on an artifact or the home page every 10 minutes and make sure its accurate.' A scheduled-trigger primitive whose action is (a) run a named workflow [composes with feat-dash-workflow-viewer-editor / feat-dash-launch-workflow] or (b) send a message to a topic/DM. Headline use case: operator-configurable violet periodic self-checks (verify an artifact or Home accuracy on a timer) — not hard-coded. Needs design: a sextant scheduled-loop primitive vs leveraging an existing scheduler; the bus + dash surface; the run-workflow vs send-message action types; pause/edit/delete + drift/missed-tick behavior.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An operator can create a loop with a time interval (e.g. every 10 min) + an action
- [ ] #2 Action type (a): run a named workflow on each tick
- [ ] #3 Action type (b): send a message (topic or DM) on each tick
- [ ] #4 Loops are visible + manageable (pause / edit / delete) in the dash
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
From Lena's outbox 2026-06-17. Composes with feat-dash-workflow-viewer-editor + feat-dash-launch-workflow (TASK-161/162) — a loop runs a workflow. Use case: violet periodic accuracy checks (goal.violet). Needs design (scheduled-loop primitive + action types + dash surface).
<!-- SECTION:NOTES:END -->

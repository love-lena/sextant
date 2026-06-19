---
id: TASK-114
title: 'Dash: workflow events + artifacts should have special formatting'
status: To Do
assignee: []
created_date: '2026-06-15 22:30'
labels:
  - feature
  - dash
  - workflow
  - ux
  - 'slug:feat-workflow-event-formatting'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 109000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Topics like msg.workflow.<id>.events and workflow-related artifacts (*.run, workflow defs) currently render as plain chat/documents. They should get structured formatting: event messages should show step name + status visually (not raw JSON), and workflow artifacts should render as a pipeline view. Second part: wf-event helper should emit richer payloads — current event messages lack context about what step/transition occurred.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 msg.workflow.*.events messages render with step name + status badge, not raw text
- [ ] #2 wf-event payloads include structured fields: step, status, message (so the dash has something to format)
- [ ] #3 workflow def + run artifacts render with a pipeline/step view instead of raw JSON/markdown
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Filed from outbox 2026-06-15. Observed during workflow-task89-favicon live run. Two parts: (1) richer wf-event payloads in the harness, (2) dash special rendering for msg.workflow.* topics and workflow artifacts. Related: [[feat-goal-progress-bus-primitive]] (TASK-84).
<!-- SECTION:NOTES:END -->

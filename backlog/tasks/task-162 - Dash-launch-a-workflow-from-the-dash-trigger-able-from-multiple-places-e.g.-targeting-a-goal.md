---
id: TASK-162
title: >-
  Dash: launch a workflow from the dash — trigger-able from multiple places
  (e.g. targeting a goal)
status: To Do
assignee: []
created_date: '2026-06-17 23:10'
labels:
  - feature
  - dash
  - workflow
  - 'slug:feat-dash-launch-workflow'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 152000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Let the operator START a workflow from the dash, not just the CLI. Lena: 'ability to start a workflow via the dash, likely targeted on something like a goal. really make it trigger-able from multiple places.' Launching should be reachable from several surfaces (a goal detail, a workflow view, command palette, Home), and a launch can be TARGETED at an object like a goal (run a workflow to advance a goal/criterion). Connects to violet's cold-start-work (goal.violet) — operator or violet mobilizes work from the dash. Needs design: the launch action + targeting model + which surfaces.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An operator can start a workflow run from the dash
- [ ] #2 Launch is available from multiple surfaces (at least a goal detail + a workflow view; consider command palette + Home)
- [ ] #3 A launch can be targeted at an object (e.g. a goal/criterion) so the run is scoped to advancing it
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
From Lena's outbox 2026-06-17. 'really make it trigger-able from multiple places.' Targets a goal (run-to-advance). Connects to goal.violet cold-start-work + feat-dash-workflow-viewer-editor. Needs design.
<!-- SECTION:NOTES:END -->

---
id: TASK-161
title: 'Dash: workflow viewer / editor'
status: To Do
assignee: []
created_date: '2026-06-17 23:10'
labels:
  - feature
  - dash
  - workflow
  - 'slug:feat-dash-workflow-viewer-editor'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 151000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Surface workflows in the dash: view a workflow (its plan/steps/runs) and edit it from the UI. Today workflows are authored + run via the CLI + the agentic orchestrator; the operator has no dash-side window into them. From Lena's outbox 2026-06-17: 'feat: workflow viewer/editor in dash.' Needs design first (what is a 'workflow' in the dash data model — saved workflow scripts? the agentic-dev orchestrator? in-flight runs?) and scoping before build.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The dash shows a list/view of available workflows
- [ ] #2 An operator can open a workflow and see its definition (plan/steps) + recent runs
- [ ] #3 An operator can edit a workflow definition from the dash (scope of 'edit' decided in design)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
From Lena's outbox 2026-06-17. Needs design + scoping (workflow data model in the dash). Related: feat-dash-launch-workflow, project_agentic_dev_workflow.
<!-- SECTION:NOTES:END -->

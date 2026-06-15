---
id: TASK-100
title: 'wf-release-pr: shell-enforced open-not-merge wrapper for workflow harness'
status: To Do
assignee: []
created_date: '2026-06-15 17:39'
labels:
  - feature
  - orchestration
  - workflow
  - 'slug:feat-wf-release-pr'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 99000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
v1 harness relies on LLM compliance with the playbook to avoid gh pr merge / git push --force / tagging. Add a wf-release-pr shell helper that only allows gh pr create, blocking all other release-path operations at the shell level.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 wf-release-pr wrapper blocks all commands except gh pr create
- [ ] #2 gh pr create is allowed with any args; all other commands (merge, push, tag, force-push) are blocked with clear error
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up to TASK-97 (agentic-dev-workflow v1); ref: [[project_agentic_dev_workflow.md]], agentic-dev-workflow-notes.md line 139
<!-- SECTION:NOTES:END -->

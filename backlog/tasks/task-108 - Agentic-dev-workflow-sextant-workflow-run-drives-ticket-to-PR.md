---
id: TASK-108
title: 'Agentic dev workflow: sextant workflow run drives ticket to PR'
status: To Do
assignee: []
created_date: '2026-06-15 17:12'
labels:
  - feature
  - workflow
  - orchestration
  - 'slug:feat-agentic-dev-workflow'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 103000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A workflow that takes a backlog ticket and drives it from spec to a gated PR. The LLM orchestrator reads the ticket, plans, dispatches a coder agent, runs Codex review (multiple rounds), writes a PR brief, presents a human gate, then opens the PR. Builds on M5.4 workflow coordinator (TASK-26). Design: artifact agentic-dev-workflow-design.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant workflow run <name> executes the full plan→implement→review→brief→gate→PR pipeline
- [ ] #2 A human gate parks the workflow before the PR is opened; explicit approval required to proceed
- [ ] #3 The workflow surfaces the brief artifact for review at the gate
- [ ] #4 A clean run on a real ticket produces a mergeable PR with a gated brief
- [ ] #5 Creds-in-history leak is patched (worker history kept out of .wf-workers)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
canopus built v1 proof-of-concept; full pipeline ran to gate on TASK-62. Parked at gate awaiting lena's call on clean re-run. Artifact: agentic-dev-workflow-design, agentic-dev-workflow-playbook. Related: [[feat-m5-workflow-coordinator]] (TASK-26), [[feat-cross-machine-p-workflows]] (TASK-24)
<!-- SECTION:NOTES:END -->

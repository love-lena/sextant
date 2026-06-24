---
id: TASK-194
title: >-
  Dash redesign: no-personas sweep — ULID+function, de-name the Assistant,
  retire the Agents roster & named-conversation list
status: To Do
assignee: []
created_date: '2026-06-24 00:33'
updated_date: '2026-06-24 01:09'
labels:
  - ready-for-agent
  - lane-foundation
dependencies:
  - TASK-220
priority: medium
ordinal: 184000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Remove personas from the operator UI. Everywhere a non-operator actor shows a name or avatar becomes ULID + what the work does. The floating Assistant is de-named (generic 'Assistant', not a person's name). The Agents roster and the named-conversation nav list are retired; steering moves to goal/run topics and the single Assistant. This is the cross-cutting identity rule the redesign holds to and future surfaces must not reintroduce named-crew messaging (CONTEXT: Run/Workflow are ULID+function).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 No non-operator name or avatar renders on any surface — runs, workflows, artifacts, agents show a ULID + function label.
- [ ] #2 The floating Assistant is labelled generically ('Assistant'), not by a name.
- [ ] #3 The Agents roster view and the named-conversation nav list are removed; their entry points are gone.
- [ ] #4 Steering is via goal/run topic threads; no named-agent DM surface remains.
- [ ] #5 Review, goals, and artifacts surfaces stay functional after the sweep.
<!-- AC:END -->

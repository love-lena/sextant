---
id: TASK-245
title: Work-engine per-step model routing is cosmetic
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
labels:
  - feature
  - workengine
  - dispatcher
  - P3
  - needs-triage
  - 'slug:feat-workengine-per-step-model-routing'
dependencies: []
priority: low
ordinal: 232000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Step labels imply per-step models/providers (e.g. 'Starts with opus…', 'passes off to gpt5…') but every step runs as the dispatcher's default pi worker (claude-haiku-4-5). Nothing routes a model/provider per step. Evidence: run 01KW8J2NNZZA844WA5GDGDTJW8 — all three step workers were haiku pi. The template's model intent is fiction.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A template step can declare a model/provider and the dispatched worker actually runs on it (or the label is removed so it doesn't imply routing that doesn't happen)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. The pi recipe honors SX_AGENT_MODEL; the coordinator would need to pass a per-step model through the spawn.request → recipe.
<!-- SECTION:NOTES:END -->

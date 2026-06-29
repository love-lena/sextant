---
id: TASK-244
title: Work-engine steps don't pipe; work-step output isn't captured
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
labels:
  - feature
  - workengine
  - coordinator
  - P2
  - needs-triage
  - 'slug:feat-workengine-step-piping-and-output-capture'
dependencies: []
priority: medium
ordinal: 231000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
In a multi-step run, each step is a fresh one-shot pi worker prompted only with the run objective — it never receives the previous step's output. Evidence: run 01KW8J2NNZZA844WA5GDGDTJW8 — step 1 ("opus writing the brief") and step 2 ("passes off to gpt5 to rewrite") each wrote a DIFFERENT poem from scratch; step 2 never saw step 1. Worse, work-step output is never captured: steps 1 and 2 produced no artifacts — their poems exist only in each worker's agent.activity stream and are otherwise lost. Only the brief step persisted anything. So a 'pipeline' template neither pipes nor preserves intermediate work.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A step worker receives the prior step's output (artifact ref and/or text) so a 'pass off to / rewrite' step operates on real input
- [ ] #2 Work-step output is captured as a durable artifact attached to the run, not left only in the activity stream
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. Relates to the run executor (task-236). Design: how step output threads to the next step's prompt while keeping content opaque to the substrate.
<!-- SECTION:NOTES:END -->

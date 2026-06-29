---
id: TASK-246
title: Run-topic operator messages don't steer a run
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
labels:
  - bug
  - workengine
  - coordinator
  - dash
  - P2
  - needs-triage
  - 'slug:bug-workengine-run-topic-no-steer'
dependencies: []
priority: medium
ordinal: 233000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
An operator chat.message on a run's topic (msg.topic.run.<id>) reaches no worker. Evidence: in run 01KW8J2NNZZA844WA5GDGDTJW8 the operator posted 'Write the poem to its own artifact' on the run topic; no worker saw it (one-shot step workers don't subscribe the run topic, and it arrived after the run was done). The run thread looks like a steering channel but isn't wired to anything that acts.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An operator message on the run topic either visibly steers the run (a worker/coordinator acts on it) OR the UI makes clear the thread is a log, not a control channel
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. Relates to run.control (the real cooperative steer) vs the run-topic chat. Design call on whether run-topic chat should influence a running step.
<!-- SECTION:NOTES:END -->

---
id: TASK-246
title: Run-topic operator messages don't steer a run
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
updated_date: '2026-06-29 21:07'
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
- [ ] #1 An operator message on msg.topic.run.<id> demonstrably STEERS the active run: a worker and/or the coordinator subscribed to the run topic receives it and acts on it within the run (its agent.activity references the operator's message and the run's behavior/output changes accordingly). Proof: post an operator instruction on a LIVE run topic mid-run and show the active worker/coordinator ingesting it + the run's subsequent output reflecting it (the 01KW8J2N case: 'write it to its own artifact' -> an artifact is actually written). Flipper: operator (live) + integration test. Fake-pass guard: falsely passes if the message is merely logged/echoed in the dash with no worker/coordinator acting (the silent-ignore) — proof must show behavioral influence on the run, not display. A message arriving after the run is terminal is reported as not-applied, never silently dropped. Design pick = LIVE STEER (operator decision 2026-06-29).
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. Relates to run.control (the real cooperative steer) vs the run-topic chat. Design call on whether run-topic chat should influence a running step.

Design decision 2026-06-29 (operator): v1 = LIVE STEER — run-topic messages influence the active run; NOT the read-only-log alternative.
<!-- SECTION:NOTES:END -->

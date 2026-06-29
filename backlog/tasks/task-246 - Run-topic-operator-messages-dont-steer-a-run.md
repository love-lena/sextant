---
id: TASK-246
title: Run-topic operator messages don't steer a run
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
updated_date: '2026-06-29 20:56'
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


## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. Relates to run.control (the real cooperative steer) vs the run-topic chat. Design call on whether run-topic chat should influence a running step.
<!-- SECTION:NOTES:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An operator message on msg.topic.run.<id> NEVER silently disappears: exactly one honest behavior holds and the dash makes which unmistakable — EITHER (a) STEER: the message demonstrably influences the active run (a worker/coordinator acts on it within the run; its activity references the message), OR (b) HONEST LOG: the dash presents the run thread as an explicitly-labeled read-only log with steering only via explicit run.control verbs. Proof: post an operator message on a live run topic and show EITHER a worker/coordinator acting on it OR the dash labels the thread read-only AND a run.control verb is the working steering path. Flipper: operator (live) + mechanical (UI label / control-verb test). Fake-pass guard: falsely passes if the thread looks like a chat input but the message reaches nothing (the 01KW8J2N silent-ignore) — proof must show the message either acted-upon, or the UI visibly is NOT a control channel. Design pick (recommend b for v1) is an operator/design call; the no-silent-swallow invariant is fixed regardless.
<!-- AC:END -->

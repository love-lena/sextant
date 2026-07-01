---
id: TASK-255
title: >-
  Run worker subscribes its run topic for guaranteed same-step steer (defer from
  TASK-246)
status: To Do
assignee: []
created_date: '2026-06-29 23:52'
labels:
  - feature
dependencies: []
ordinal: 240000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-246 ships the coordinator-as-router steer path (coordinator subscribes msg.topic.run.<id>, DMs the active worker's inbox + threads into the next step). That meets the AC. This follow-up adds the SECOND mechanism: the dispatched run-step worker subscribes its run topic DIRECTLY so an operator post wakes a turn it incorporates with no coordinator round-trip — closing the timing gap where a steer routed via the coordinator's DM lands after the worker already emitted step-done (so it influences the next step, not the current one). Scope: pi-bus/src (lift a RUN_TOPIC directive into watchTopics) + clients/dispatcher/recipes/pi.sh (+ the CLI embed copy, drift guard). MUST reconcile with TASK-118: the worker subscribing a bus topic is fine, but confirm it works under the srt sandbox (egress/process policy). Additive; no run/v1 contract change (RunTopicSubject already exists from TASK-246).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A dispatched run-step worker subscribes msg.topic.run.<id> and an operator post there influences the CURRENT step (not only the next), proven by a test/live run where the steer changes the in-flight step's output; works under TASK-118 sandbox mode
<!-- AC:END -->

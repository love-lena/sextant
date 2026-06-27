---
id: TASK-235
title: pi --rpc sessions emit their live work stream to a bus channel
status: To Do
assignee: []
created_date: '2026-06-25 03:06'
updated_date: '2026-06-27 00:10'
labels:
  - feature
  - pi
  - observability
  - agent-monitoring
  - work-engine
  - bus
  - ready-for-human
  - 'slug:feat-pi-rpc-work-stream-to-bus'
  - P1
dependencies:
  - TASK-151
priority: high
ordinal: 224000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
pi --rpc worker sessions (the drain-and-revive mobilized one-shot workers, ADR-0045) run opaquely today — there is no live visibility into what a worker is doing as it executes. Each pi --rpc session should publish its live work stream (tool-uses, messages, turn/step events) to a bus channel in real time, so the dash and the operator can watch the worker as it runs. This is the pi-harness instance of the general activity-tap seam in TASK-151 (which builds the claude -p stream-json / CC-hook tap and an adapter seam for 'other harnesses' — pi is one of those). It is also the observability producer the run executor needs: when the executor dispatches a pi --rpc worker for a run's work step, the worker's stream should flow to that run's channel (e.g. msg.topic.run.<id>), lighting up the run activity log and the 'steer this run' view that today have no producer (TASK-236, and the dead run-topic post).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A pi --rpc session publishes its live work stream (tool-uses + messages + turn/step events) to a defined bus subject in real time, under a documented record shape
- [ ] #2 The stream shape conforms to / maps onto the common activity-event shape from TASK-151's adapter seam (no divergent per-harness format)
- [ ] #3 Emission is cheap, does not block the worker's progress, and degrades gracefully when the bus is unreachable
- [ ] #4 The activity stream is GENERIC to the agent: published to a per-agent subject (e.g. agent.activity.<id>), NEVER bound to a run/goal/workflow. Correlating a stream to a run is a downstream concern (map agent-id -> run), not the producer's job
- [ ] #5 The stream is the FULL raw event stream (the pure noise): every tool-use, message, and turn/step event, unfiltered — for audit + under-the-hood debugging, not a curated summary
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Tap the pi --rpc event stream, normalize to the common activity record (shared with TASK-151), publish to the bus subject (run topic when bound to a run, else agent.activity.<id>); mirror the claude tap in 151.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Requested by Lena 2026-06-24 as part of the work-engine fixes. The pi-harness sibling of [[feat-monitor-agent-activity-stream]] (TASK-151). Feeds the run executor's activity log + steer view [[feat-run-executor-workflow-run-v1]] (TASK-236) — composes with it, not blocked by it. Relates ADR-0045 (drain-and-revive pi --rpc workers), [[task-177]] (pi-bus extension), [[task-176]] (pi spike), [[feat-agent-status-thinking-on-turn-start]] (TASK-150).

Design decision (Lena, 2026-06-26): the live activity feed = EVERYTHING, the pure noise — for audit + seeing under the hood. It is GENERIC to agents, never tied to a run/goal/workflow. This SUPERSEDES the original AC #2 (stream lands on the run's channel msg.topic.run.<id>), now removed — a pi --rpc worker publishes to its own per-agent subject ALWAYS. The run executor's run.event (step-done/outcome signal, [[feat-run-executor-workflow-run-v1]] TASK-236) is a SEPARATE low-volume stream on a different topic; the activity feed and run.event never share a channel. Priority raised to P1 + wanted FIRST: Lena wants the feed working before the executor, to make debugging workflow runs easier. Shares the common activity-event shape with [[feat-monitor-agent-activity-stream]] (TASK-151, the claude/generic seam); whichever producer lands first defines that shape.
<!-- SECTION:NOTES:END -->

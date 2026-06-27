---
id: TASK-235
title: pi --rpc sessions emit their live work stream to a bus channel
status: Done
assignee: []
created_date: '2026-06-25 03:06'
updated_date: '2026-06-27 23:51'
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
- [x] #1 A pi --rpc session publishes its live work stream (tool-uses + messages + turn/step events) to a defined bus subject in real time, under a documented record shape
- [x] #2 The stream shape conforms to / maps onto the common activity-event shape from TASK-151's adapter seam (no divergent per-harness format)
- [x] #3 Emission is cheap, does not block the worker's progress, and degrades gracefully when the bus is unreachable
- [x] #4 The stream is the FULL raw event stream (the pure noise): every tool-use, message, and turn/step event, unfiltered — for audit + under-the-hood debugging, not a curated summary
- [x] #5 Subject is msg.agent.<id>.activity (entity.id.aspect, matching msg.workflow.<id>.events / .control). Generic per-agent — NEVER bound to a run/goal/workflow. Wildcards: msg.agent.*.activity = one agent, msg.agent.> = all
- [x] #6 Built by PROMOTING the existing pi.activity (lexicon protocol/lexicons/pi.activity.json + @sextant/pi-bus, TASK-177; emits turn_start/turn_end/tool_start/tool_end/thinking/message) to a harness-neutral agent.activity lexicon on the generic subject — NOT greenfield. pi-bus publishes onto it; future harnesses (TASK-151 claude tap) emit the same shape
- [x] #7 turn_end events on this stream are the 'worker came to rest' signal the run executor (TASK-236) consumes to enforce no-stop-without-output (post-or-revive at each rest). So this feed lands BEFORE the executor
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Tap the pi --rpc event stream, normalize to the common activity record (shared with TASK-151), publish to the bus subject (run topic when bound to a run, else agent.activity.<id>); mirror the claude tap in 151.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Requested by Lena 2026-06-24 as part of the work-engine fixes. The pi-harness sibling of [[feat-monitor-agent-activity-stream]] (TASK-151). Feeds the run executor's activity log + steer view [[feat-run-executor-workflow-run-v1]] (TASK-236) — composes with it, not blocked by it. Relates ADR-0045 (drain-and-revive pi --rpc workers), [[task-177]] (pi-bus extension), [[task-176]] (pi spike), [[feat-agent-status-thinking-on-turn-start]] (TASK-150).

Design decision (Lena, 2026-06-26): the live activity feed = EVERYTHING, the pure noise — for audit + seeing under the hood. It is GENERIC to agents, never tied to a run/goal/workflow. This SUPERSEDES the original AC #2 (stream lands on the run's channel msg.topic.run.<id>), now removed — a pi --rpc worker publishes to its own per-agent subject ALWAYS. The run executor's run.event (step-done/outcome signal, [[feat-run-executor-workflow-run-v1]] TASK-236) is a SEPARATE low-volume stream on a different topic; the activity feed and run.event never share a channel. Priority raised to P1 + wanted FIRST: Lena wants the feed working before the executor, to make debugging workflow runs easier. Shares the common activity-event shape with [[feat-monitor-agent-activity-stream]] (TASK-151, the claude/generic seam); whichever producer lands first defines that shape.

Refinement (Lena, 2026-06-26): subject decided = msg.agent.<id>.activity (idiomatic entity.id.aspect, parallels msg.workflow.<id>.events; supersedes the placeholder 'agent.activity.<id>'). Investigation: pi.activity ALREADY EXISTS and is ~80% this feed — lexicon protocol/lexicons/pi.activity.json, producer @sextant/pi-bus (clients/pi-bus, TASK-177), default subject msg.topic.pi.activity.<id> via SEXTANT_ACTIVITY_TOPIC. Only the NAME + subject are pi-coupled; shape (kind/turnIndex/tool/args/result/text) is already generic. So TASK-235 = PROMOTE pi.activity -> harness-neutral agent.activity on msg.agent.<id>.activity (rename lexicon + repoint pi-bus subject), NOT build from scratch. No agent.activity lexicon exists yet. Dual payoff: this same stream's turn_end is the rest-detection signal the run executor needs to enforce the no-stop-without-output rule (revive-on-empty-rest, ADR-0045 drain-and-revive) — there is NO other 'worker exited/at-rest' bus signal today (dispatcher marks ag.running=false internally only; agent.status idle is stubbed/off-bus TASK-84; pi.handoff.relinquished only after explicit drain).

Implemented on worktree-task-235-agent-activity (PR pending). PROMOTED pi.activity → harness-neutral agent.activity: (1) lexicon protocol/lexicons/agent.activity.json supersedes pi.activity.json; (2) @sextant/pi-bus emits $type:agent.activity on the per-agent subject msg.agent.<id>.activity (override SEXTANT_ACTIVITY_TOPIC still wins; default path now exercised by the driven/live-demo harnesses); (3) dash labels the stream '<agent> · activity' (already captured generically via msg.>); (4) new Go convention conventions/agentactivity/go — Activity record + ParseActivity + ActivitySubject(id), the parse side TASK-236's rest-detection imports. Verb-less record, so the verb-based conformance harness needs no vectors; Go record + pi-bus TS ActivityRecord are the two co-equal peers. Live-proved hermetically with real pi 0.80.2 over a real NATS bus: 15 frames on msg.agent.<id>.activity, all six kinds incl. turn_end (the TASK-236 rest signal); pi-bus 30/30 unit, go test + make lint green. Also fixed the stale build:deps paths (pre-ADR-0049 layout).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Promoted the pi-only activity stream to the harness-neutral agent.activity feed on msg.agent.<id>.activity (lexicon + @sextant/pi-bus producer + dash label + a Go parse-side convention conventions/agentactivity/go). Live-proved end-to-end with real pi over a real NATS bus (15 frames, all six kinds incl. turn_end — the rest signal TASK-236 consumes). All 7 ACs met. Landed via worktree-task-235-agent-activity. Follow-on: TASK-151 (claude-harness producer of the same shape) and TASK-236 (run executor consuming turn_end).
<!-- SECTION:FINAL_SUMMARY:END -->

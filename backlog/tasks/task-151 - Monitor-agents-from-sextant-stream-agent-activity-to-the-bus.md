---
id: TASK-151
title: Monitor agents from sextant — stream agent activity to the bus
status: To Do
assignee: []
created_date: '2026-06-17 18:44'
updated_date: '2026-06-27 23:51'
labels:
  - feature
  - dash
  - observability
  - agent-monitoring
  - 'slug:feat-monitor-agent-activity-stream'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 141000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena wants to monitor agents from sextant (outbox, 2026-06-17): see live what each agent is doing. Her question: do claude code / claude -p expose a stream of their activity, and what about other harnesses? Findings: YES — `claude -p` supports `--output-format stream-json`, a real-time JSONL event stream (assistant text, tool_use, tool_result, final result). Interactive Claude Code exposes lifecycle HOOKS (PreToolUse / PostToolUse / UserPromptSubmit / Stop / …) — already the mechanism behind the per-agent agent.status hook (#132). Both give a tappable activity stream. Other harnesses (codex, aider, …) vary and each need a thin adapter mapping their native stream to a common bus shape. Feature shape: a per-harness 'activity tap' publishing an agent's tool-uses + messages to the bus, so the dash renders a live per-agent activity feed — the deeper view behind agent.status (the summary pill) and TASK-150 (the thinking indicator).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A claude-based agent's activity (tool calls + messages) is published to the bus in real time via `claude -p --output-format stream-json` (or CC PostToolUse hooks), under a defined activity subject/record shape
- [ ] #2 The dash renders a live per-agent activity feed from that stream
- [ ] #3 A documented adapter seam so non-claude harnesses (codex, etc.) can map their native event stream to the same bus shape
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Tap claude -p --output-format stream-json (and/or CC PostToolUse hooks) -> normalize -> publish activity events to a bus subject (e.g. agent.activity.<id>, or msg.* + an activity record) -> dash per-agent feed. Layer over agent.status (#132) summary + TASK-150 thinking indicator; per-harness adapters behind a common event shape.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Lena outbox 2026-06-17. claude -p stream-json + CC hooks are the claude tap; other harnesses need per-harness adapters. Relates: [[feat-agent-status-thinking-on-turn-start]] (TASK-150), the per-agent status hook (#132), the dash stream-explorer idea.

Design decision (Lena, 2026-06-26): this live agent-activity feed = EVERYTHING, the pure noise — for audit + under-the-hood debugging — and is GENERIC to agents: per-agent subject (agent.activity.<id>), never coupled to a run/goal/workflow. The pi --rpc producer ([[feat-pi-rpc-work-stream-to-bus]] TASK-235) is wanted FIRST (raised to P1), ahead of the run executor (TASK-236), to make debugging workflow runs easier. 151 and 235 share ONE common activity-event shape; whichever lands first defines it (pi is the priority producer). Keep the feed decoupled from the executor's run.event step-done signal — separate streams/topics.

Refinement (Lena, 2026-06-26): the common activity-event shape = a harness-neutral agent.activity lexicon on subject msg.agent.<id>.activity (idiomatic entity.id.aspect, parallels msg.workflow.<id>.events). It is PROMOTED from the existing pi.activity (protocol/lexicons/pi.activity.json + @sextant/pi-bus, TASK-177) — pi is the first producer ([[feat-pi-rpc-work-stream-to-bus]] TASK-235, lands first). This claude tap (claude -p stream-json / CC PostToolUse) is a second producer that emits the SAME agent.activity shape onto the same subject — that is the 'documented adapter seam' (AC #3): all harnesses converge on agent.activity, no per-harness format.

Update (TASK-235 landed, 2026-06-27): the harness-neutral agent.activity shape + subject msg.agent.<id>.activity are now LIVE — promoted from pi.activity (lexicon protocol/lexicons/agent.activity.json; Go parse-side conventions/agentactivity/go; @sextant/pi-bus is the first producer). The shape is fixed (turn_start|turn_end|tool_start|tool_end|thinking|message; see the lexicon). TASK-151's remaining scope = the claude-harness producer (claude -p stream-json / CC PostToolUse) emitting the SAME record on the SAME subject — that is the 'documented adapter seam' (AC #3). No per-harness format.
<!-- SECTION:NOTES:END -->

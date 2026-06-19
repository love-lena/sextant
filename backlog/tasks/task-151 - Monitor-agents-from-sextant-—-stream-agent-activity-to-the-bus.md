---
id: TASK-151
title: Monitor agents from sextant — stream agent activity to the bus
status: To Do
assignee: []
created_date: '2026-06-17 18:44'
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
<!-- SECTION:NOTES:END -->

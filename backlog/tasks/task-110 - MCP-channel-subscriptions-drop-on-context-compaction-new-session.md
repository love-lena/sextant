---
id: TASK-110
title: MCP channel subscriptions drop on context compaction (new session)
status: To Do
assignee: []
created_date: '2026-06-15 19:10'
labels:
  - bug
  - mcp
  - channels
  - 'slug:bug-mcp-channel-subs-drop-on-compaction'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 105000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When a Claude Code session compacts (context overflow → summarized restart), any message_subscribe channel registrations from the prior session are silently lost. The resumed session starts with no active subscriptions and misses live bus frames until manually resubscribing. The agent has no awareness that subscriptions were previously active — the first symptom is the principal noticing the agent has gone quiet. Root cause: CC channel events are session-scoped; compaction creates a new session context, which drops the subscription state.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 After compaction/session restart, channel subscriptions are either auto-restored by the MCP plugin or the session-start hook fires a reconnect that resubscribes all previously-active topics
- [ ] #2 If auto-restore is not feasible, the plugin should emit a startup notice listing topics that were active before the gap so the agent can resubscribe explicitly
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Investigate whether Claude Code MCP sessions expose a stable session-ID across compaction (similar to CLAUDE_CODE_SESSION_ID for spawned agents). If stable: persist subscription set keyed on session-ID and restore on reconnect. If not stable: the SessionStart hook is the best intervention point — add logic to resubscribe known topics.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered 2026-06-15 when sirius lost all subscriptions after context compaction; principal noticed the agent went silent. Workaround: manual unsub + resub cycle restores delivery. Related: [[feat-named-agent-identity-stable-reconnect]] (TASK-76 — same compaction/restart pain point).
<!-- SECTION:NOTES:END -->

---
id: TASK-129
title: 'Dash: show agent status at the bottom of DM views (above the input)'
status: To Do
assignee: []
labels:
  - feature
  - dash
  - ux
  - presence
  - 'slug:feat-dash-agent-status-in-dm'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (2026-06-16): in a DM conversation view, surface the OTHER party's
per-agent status (the Haiku self-report `agent.status` / `status.<agent-id>`
artifact — state + headline, e.g. "working · drafting the heartbeat PR") at the
BOTTOM of the DM, sitting right above the input box. So when you're DM'ing an
agent you can see at a glance what it's doing.

Scope: **DMs only for now.** Explicitly extensible later to presence info in all
subscriptions/conversations, but start with DMs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A DM conversation view shows the other party's agent.status (state + headline) in a strip directly above the composer/input box
- [ ] #2 It reads the live `status.<agent-id>` artifact for the DM's counterpart + updates as that status changes
- [ ] #3 Degrades gracefully when the counterpart has no agent.status (human, or an agent with no status yet) — no broken strip
- [ ] #4 Scoped to DMs for v1; the component is written so it can later extend to topic/other conversations (presence in all subs)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Frontend (the DM conversation stage in app.jsx + read status.<id>). The
agent.status primitive shipped in v0.4.0 (#132): per-agent `status.<agent-id>`
latest-value artifact, state enum idle|working|waiting-for-human|... + headline.
For a DM msg.topic.dm.<a>.<b>, the counterpart is whichever id isn't self →
read status.<counterpart>. Orion's dash lane (single-writer app.jsx). Will
compose nicely with the TASK-126 heartbeat last-seen presence once that lands
(the "extend to all subs" path). Discovered: Lena 2026-06-16.
<!-- SECTION:NOTES:END -->

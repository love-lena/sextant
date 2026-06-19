---
id: TASK-138
title: Wire the floating Assistant's brain (real answers over bus state)
status: To Do
assignee: []
created_date: '2026-06-16 21:36'
updated_date: '2026-06-16 23:46'
labels:
  - feature
  - dash
  - assistant
  - llm
  - design
  - 'slug:feat-wire-assistant-brain'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 128000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Live Flow Assistant is a visible STUB in v0.5 (no LLM endpoint). orion's reskin builds the FLOW — the FAB panel + the ⌘K no-match -> 'Ask the assistant: <text>' entry that opens it with the prompt — but it shows 'not wired yet'. This ticket = wire the BRAIN: a universal helper that answers quick questions about current state (goals, what's waiting on you, where a workflow stands), READ-ONLY over the bus, distinct from the work agents. Design: scope (read-only; what it sees — goals/artifacts/agents/messages), can-it-act (read-only vs spawn-a-doc), the model/endpoint + per-session identity + cost. v0.5's 'own track' (post the Track-1 reskin) — pull in if wanted.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The Assistant answers quick questions about current bus state (read-only) — not a stub
- [ ] #2 The ⌘K 'Ask the assistant: <text>' entry (built in the reskin) routes to a real answer
- [ ] #3 The Assistant's scope (read-only; sources), identity, and model are explicit
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
#ui-feedback (2026-06-16). The reskin ships the FAB + the ⌘K-entry flow (orion, stage b); this wires the brain. v0.5 Assistant was scoped as a stub / own track. Related: v0-5-dash-design, v0-5-charter.

UPDATE 2026-06-16 (Lena): re-scoped + now v0.5.0 SCOPE. The Assistant is 'just a specially designated CLIENT you message' — the floating panel = a DM to that bus client (an agent), NOT a bespoke LLM endpoint. Simpler + bus-native: designate a client as 'the assistant', wire the FAB to DM it, the agent reads the workspace + replies. May be the SAME agent as TASK-144 (the attention-defending/curation agent) — 'your agent' that defends your inbox proactively AND answers when messaged. Design pass before building.
<!-- SECTION:NOTES:END -->

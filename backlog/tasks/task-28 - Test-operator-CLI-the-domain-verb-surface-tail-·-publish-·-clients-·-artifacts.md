---
id: TASK-28
title: >-
  Test/operator CLI: the domain-verb surface (tail · publish · clients ·
  artifacts)
status: To Do
assignee: []
created_date: '2026-06-04 18:11'
labels: []
milestone: 'M2: MVP'
dependencies: []
references:
  - docs/adr/0014-the-tui-is-a-client.md
priority: high
ordinal: 27000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Replaces the dash as M2's human surface (the dash moved to M4). A CLI sufficient to fully e2e-test the MVP by hand: tail/observe the dialogue, publish a message, list connected clients, and get/put/update/watch artifacts. Crucially this is the SAME domain-verb surface the MCP server + skill expose (TASK-22) — define the verbs ONCE (they map 1:1 to the SDK: Publish, Subscribe, ListClients, Create/Update/Get/Watch Artifact) and surface them three ways: CLI subcommands, MCP tools, and skill docs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant tail streams live messages (with replay) for end-to-end observation — 'tail the NATS messages'
- [ ] #2 sextant publish sends a message; sextant clients lists the registry; sextant artifact get/put/update/watch covers artifacts
- [ ] #3 The verb surface is defined once and shared with the MCP server + skill (TASK-22): CLI subcommands and MCP tools expose the same verbs
- [ ] #4 The CLI alone is sufficient to fully exercise an MVP e2e test (bus up + two manual clients communicating, observed via the CLI)
<!-- AC:END -->

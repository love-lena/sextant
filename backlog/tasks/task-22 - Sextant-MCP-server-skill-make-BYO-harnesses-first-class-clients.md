---
id: TASK-22
title: 'Sextant MCP server + skill: make BYO harnesses first-class clients'
status: To Do
assignee: []
created_date: '2026-06-04 17:52'
updated_date: '2026-06-04 17:56'
labels: []
milestone: 'M2: MVP'
dependencies: []
references:
  - docs/adr/0003-high-level-architecture.md
  - docs/adr/0008-clients-are-processes.md
  - docs/adr/0012-reserved-namespace-and-authn.md
priority: high
ordinal: 21000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The 'reference harness' gap (ADR-0003: the harness is bring-your-own) is best closed NOT by shipping a bespoke harness, but by giving any existing harness — Claude Code, Codex, etc. — a turnkey way to speak the protocol. Two mechanisms, decide skill vs MCP vs both: (1) an MCP server exposing the Go SDK's domain verbs as MCP tools (identity/connect, publish + subscribe messages, create/update/get/watch artifacts, ListClients, emit spawn-requests, workflow control) so any MCP-capable agent becomes a sextant client under its own verified identity (ADR-0012); (2) a skill that teaches an agent the sextant conventions and when to reach for each verb. This is the MVP's agent-integration path — it makes BYO viable and is what lets the rest of the reference fleet (dispatcher, coordinator, dash) actually have agents to coordinate. MVP-phase, sits after the M1 core (which is done).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Decide the surface: MCP tools, a skill, or both (with rationale)
- [ ] #2 MCP server exposes the SDK domain verbs (messages, artifacts, clients registry, spawn-request) under per-client identity (ADR-0012)
- [ ] #3 A BYO harness (e.g. Claude Code) can join the bus, exchange messages, and share artifacts with no bespoke harness code
- [ ] #4 Skill (if included) documents the conventions + verb-selection guidance
<!-- AC:END -->

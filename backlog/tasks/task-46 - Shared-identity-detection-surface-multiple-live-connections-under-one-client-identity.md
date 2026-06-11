---
id: TASK-46
title: >-
  Shared-identity detection: surface multiple live connections under one client
  identity
status: To Do
assignee: []
created_date: '2026-06-11 00:42'
labels:
  - needs-triage
dependencies: []
priority: low
ordinal: 52000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two processes wearing the same creds (e.g. two Claude Code sessions resolving the same active context) silently share an author ULID — collaborators DM one "client" that is invisibly two agents (ADR-0008: clients are processes; ADR-0012: one server = one verified identity). The TASK-22 design wanted a launch-time duplicate-identity guard in the MCP server, but it is unimplementable against the current surface: clients.list presence is binary, and any query already requires a connection under the same identity, so "online before this process joined" is unobservable. A real guard needs the core to expose connection multiplicity (e.g. a connection count or per-connection rows derived from Connz, alongside the presence flag) — a locked-core protocol change (ADR-0022), serial work.

Scope: expose enough in clients.list (or a sibling read) for any client to detect that its identity has more than one live connection; then consumers (sextant-mcp, dash) warn loudly. Fail-loud, not fail-stop — sharing an identity must not brick a session.

Origin: cut from the TASK-22 spec on review (.work/rfcs/rfc-task-22-claude-plugin.md); the skill rule "one context per agent" is the convention-level mitigation in the meantime.
<!-- SECTION:DESCRIPTION:END -->

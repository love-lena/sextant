---
id: TASK-76
title: >-
  Sirius (and other named agents) should connect under their registered
  identity, not the auto-minted session ID
status: To Do
assignee: []
created_date: '2026-06-13 03:30'
labels:
  - feature
  - identity
  - mcp
  - ergonomics
  - 'slug:feat-named-agent-stable-identity'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 81000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Sirius is registered as a named client ('sirius', 01KTYFK00J6RXP4CFPHPWRBRS1) but the MCP server auto-mints a fresh identity per session (claude-cd37cd47...). So messages from sirius appear under a raw session ID, not the name. The fix: the MCP server plugin needs to be configured with sirius's creds file so it connects as the registered identity across sessions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The MCP plugin for a named agent (sirius, and equivalently other named crew) connects to the bus under its registered named identity, not a fresh auto-minted session ID
- [ ] #2 The operator has a documented path to configure this (point the plugin at a specific creds file via plugin config or sextant context)
- [ ] #3 Reconnects + --resume sessions preserve the named identity
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Likely: add a SEXTANT_CREDS env var or plugin.json config field that the MCP server reads instead of auto-minting. Connects to the context system (sextant context use) and ADR-0029.
<!-- SECTION:PLAN:END -->

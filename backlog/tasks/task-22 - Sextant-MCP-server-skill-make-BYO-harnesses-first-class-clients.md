---
id: TASK-22
title: 'Sextant MCP server + skill: make BYO harnesses first-class clients'
status: To Do
assignee: []
created_date: '2026-06-04 17:52'
updated_date: '2026-06-04 21:38'
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
Make any BYO harness a first-class sextant client (ADR-0003, ADR-0012) by shipping a Claude Code plugin: an MCP server that is also a channel, plus skill(s). One server = one verified identity (creds at launch; ADR-0012). The MCP server exposes the one-shot + pull-batch verbs as MCP tools (message.publish, message.read, clients.list, artifact create/update/get/delete) AND declares claude/channel to push inbound bus messages into the session as <channel sender=.. subject=..> tags; the reply path is the message.publish verb. So the agent gets multiple inbound options: pull (message.read) and push (channel). Skill(s) teach sextant conventions + verb selection and how to stand up an ad-hoc Monitor over 'sextant subscribe' for live observation (no first-class plugin monitor). Caveat: channels are research-preview + allowlist-gated - fine for own use (--dangerously-load-development-channels), broad distribution needs Anthropic allowlisting; message.read stays the portable floor for non-Claude-Code harnesses. Full design: .work/rfcs/rfc-m2-verb-surface.md.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Decided (was AC1 'MCP vs skill vs both'): both, plus the channel, packaged as a plugin. Shares ONE verb surface with the CLI (TASK-28) and the protocol source-of-truth (TASK-12); the conformance test (TASK-28) pins CLI+MCP parity. Channels folded in here, not a future ticket. Identity: single per server; duplicate-identity = block-with --reclaim (robust dedup -> TASK-20).
<!-- SECTION:NOTES:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The sextant MCP server exposes the one-shot + pull-batch verbs as tools under one verified identity (ADR-0012)
- [ ] #2 The MCP server declares claude/channel and pushes inbound bus messages as <channel> events; reply path = message.publish
- [ ] #3 A skill documents sextant conventions, verb selection, and the ad-hoc 'sextant subscribe' Monitor recipe
- [ ] #4 Packaged as an installable Claude Code plugin (MCP-channel + skill); a BYO harness joins, exchanges messages, and shares artifacts with no bespoke code
<!-- AC:END -->

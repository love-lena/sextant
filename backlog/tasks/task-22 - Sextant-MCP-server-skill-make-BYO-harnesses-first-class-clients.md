---
id: TASK-22
title: 'Sextant MCP server + skill: make BYO harnesses first-class clients'
status: To Do
assignee: []
created_date: '2026-06-04 17:52'
updated_date: '2026-06-11 00:02'
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

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The sextant MCP server exposes the one-shot + pull-batch verbs as tools under one verified identity (ADR-0012)
- [ ] #2 The MCP server declares claude/channel and pushes inbound bus messages as <channel> events; reply path = message.publish
- [ ] #3 A skill documents sextant conventions, verb selection, and the ad-hoc 'sextant subscribe' Monitor recipe
- [ ] #4 Packaged as an installable Claude Code plugin (MCP-channel + skill); a BYO harness joins, exchanges messages, and shares artifacts with no bespoke code
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Dogfood learnings for the MCP/skill (2026-06-09, claude as a live CLI client during the dash demo — exactly the BYO-harness experience TASK-22 should productize):

1. PRESENCE NEEDS A HELD CONNECTION. 'Online' = an open subscription. A CLI/MCP client that connects per-call reads as offline between calls and its human collaborator notices ('hi! you there?'). The MCP server should hold ONE long-lived connection + subscription for the session, not dial per tool-call.

2. LIVE TAIL IS THE HARD PART for an agent harness. Subscribe-with-replay into a file + a watcher pipeline is fragile (two buffering bugs in one day: pre-arm lines skipped, then a block-buffering pipe stage silently sitting on events). The MCP server should expose (a) read-since (catch up explicitly by last-seen id) and (b) a push/notification path, so the agent never builds its own tail pipeline.

3. STALE PINNED URLs strand every client the same way (ADR-0025 fixed the root cause); MCP server should resolve via context + store discovery with a loud, actionable error naming both the URL it tried and where it came from.

4. RECORD SHAPE must be discoverable: composing a chat.message required reading pkg/tui/surface/records.go for the $type/text lexicon. The skill should carry the common lexicon shapes (chat.message at minimum) so an agent can publish without spelunking.

5. AUTHOR IDENTITY: messages carry the bus-stamped author id; mapping id→display name requires clients.list. The MCP read path should resolve display names server-side (or the skill should document the join).

2026-06-10: the verb surface grew artifact.list in #99 — add it to the MCP tool list (AC#1). The CLI side of the parity conformance test (TestCLIMatchesOperations, cmd/sextant/conformance_test.go) shipped in #99; AC for MCP parity extends it rather than starting fresh.
<!-- SECTION:NOTES:END -->

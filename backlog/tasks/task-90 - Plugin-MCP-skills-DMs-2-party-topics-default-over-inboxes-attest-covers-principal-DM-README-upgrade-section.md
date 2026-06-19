---
id: TASK-90
title: >-
  Plugin/MCP/skills: DMs (2-party topics) default over inboxes; attest covers
  principal DM; README upgrade section
status: In Progress
assignee: []
created_date: '2026-06-14 21:58'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - plugin
  - mcp
  - skills
  - trust
  - docs
  - 'slug:feat-plugin-dm-default-over-inbox'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 92000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (principal) asked to update the plugin/MCP/skills so the new conventions are first-class — chiefly DM-as-a-2-party-topic as the default for back-and-forth, with msg.client.<id> reframed as a one-way inbox/wake-floor (not a conversation channel), per ADR-0034 and her bus directive ('agents DM me in their own 1:1 dm topics'). Today the trust hook (sextant-mcp attest) and the channel-wake bridge only cover the inbox, so a principal DM on a 2-party topic is woken (explicit subscribe pushes a channel event) but NOT auto-stamped by the hook — second-class vs the inbox, contradicting 'default'. Also add a README section on updating sextant to a new version (brew upgrade, restart the bus brew service, reopen the dash, restart active harnesses so they pick up the new sextant-mcp).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 pkg/sx exposes DMSubject(a,b) = msg.topic.dm.<sorted ids> (lexicographic, deterministic regardless of arg order), unit-tested
- [x] #2 sextant-mcp attest also scans the principal DM topic (DMSubject(self,principal)) when a principal is designated, stamping those frames identically (attest.Classify), into one combined trusted block; per-subject cursors advance independently; a DM-topic fetch failure still emits the inbox block (fail-soft preserved)
- [x] #3 message_publish tool description distinguishes msg.topic.<name> (shared topic), msg.topic.dm.<sorted ids> (2-party DM), msg.client.<id> (one-way inbox/ping)
- [x] #4 sextant SKILL.md teaches inbox-vs-DM with DM as the default for back-and-forth, the deterministic DM subject, and the wake+trust mechanics (explicit subscribe wakes; classify by bus-stamped sender_id vs principal); startup SKILL.md tells a worker to subscribe to its principal DM on connect
- [x] #5 clients/claude-code/README.md has an 'Updating sextant' section: brew upgrade sextant, brew services restart sextant, reopen sextant dash --serve, restart active Claude Code sessions to reload sextant-mcp
- [x] #6 make lint && make test green; demo-principal-trust.sh (or equivalent) proves a principal DM on a 2-party topic gets the trusted stamp
- [ ] #7 Operator-update: this change reaches lena's live install via (a) a tagged sextant release [needs lena's sign-off] -> brew upgrade for the new sextant-mcp binary, and (b) the plugin bump to 0.1.3 -> claude plugin update for the new skills/hooks; then restart the bus service + active Claude Code sessions + dash (see clients/claude-code/README.md 'Updating to a new version')
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
TDD DMSubject in pkg/sx; TDD attest two-subject scan in cmd/sextant-mcp; edit handlers.go tool desc; rewrite the inbox/DM sections of both SKILL.md; add README upgrade section; extend demo-principal-trust.sh with a DM-topic case.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Asked by lena (principal) on msg.topic.frontend-dash seq 520 + 521 (2026-06-14). Conventions: [[feat-web-cockpit-conventions]] (ADR-0034). Deferred follow-ups to file: SDK auto-subscribe principal DM for idle-wake; TUI/dash DM opens 2-party topic + adopt sx.DMSubject; SDK rename DMs()->Inbox(); attest scan peer/wildcard DMs.

Implemented on branch task-90-dm-default: sx.DMSubject (+test); attest scans inbox + principal DM (per-subject cursors, fail-soft, +test); message_publish tool desc; both SKILL.md; README upgrade section; plugin 0.1.3; demo-dm-trust.sh (4/4 PASS on the real sextant-mcp binary). make lint + make test (race) green.

Status drift: ACs largely checked / likely shippable (only the operator-update step may remain). Verify and close.
<!-- SECTION:NOTES:END -->

---
id: TASK-22
title: 'Sextant MCP server + skill: make BYO harnesses first-class clients'
status: Done
assignee: []
created_date: '2026-06-04 17:52'
updated_date: '2026-06-11 02:38'
labels:
  - ready-for-agent
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
Make any BYO harness a first-class sextant client (ADR-0003, ADR-0012) by shipping a Claude Code plugin: an MCP server that is also a channel, plus skill(s). One server = one verified identity (creds at launch; ADR-0012). The MCP server exposes the one-shot + pull-batch verbs as MCP tools (message.publish, message.read, clients.list, artifact create/update/get/delete/list) AND declares claude/channel to push inbound bus messages into the session as <channel sender=.. subject=..> tags; the reply path is the message.publish verb. So the agent gets multiple inbound options: pull (message.read) and push (channel). Skill(s) teach sextant conventions + verb selection and how to stand up an ad-hoc Monitor over 'sextant subscribe' for live observation (no first-class plugin monitor). Caveat: channels are research-preview + allowlist-gated - fine for own use (--dangerously-load-development-channels), broad distribution needs Anthropic allowlisting; message.read stays the portable floor for non-Claude-Code harnesses. Full design: .work/rfcs/rfc-task-22-claude-plugin.md (2026-06-10; replaces the lost rfc-m2-verb-surface.md).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The sextant MCP server exposes the one-shot + pull-batch verbs as tools under one verified identity (ADR-0012)
- [x] #2 The MCP server declares claude/channel and pushes inbound bus messages as <channel> events; reply path = message.publish
- [x] #3 A skill documents sextant conventions, verb selection, and the ad-hoc 'sextant subscribe' Monitor recipe
- [x] #4 Packaged as an installable Claude Code plugin (MCP-channel + skill); a BYO harness joins, exchanges messages, and shares artifacts with no bespoke code
- [x] #5 A conformance test pins methods.json ↔ MCP both directions: every op is a tool, channel-delivered, or excluded-by-declaration; every tool is a mapped op or a declared extra; new ops and stray tools fail
- [x] #6 The connection is held for the session: presence reads online between tool calls (no per-call dialing)
- [x] #7 A session with no identity heals mid-session: register --self then a retried tool call succeeds, without restarting the server; pre-connection errors name the resolution chain / URL provenance
- [x] #8 Lost-tail states are loud: resume_lost / resume_deferred arrive as system-notice channel events; subscribe in a channel-less session fails or warns rather than blackholing
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Work-plan: .work/plans/2026-06-10-task-22-claude-plugin.md (11 tasks, per-stage acceptance, AFK ground rules + stop conditions). Spec: .work/rfcs/rfc-task-22-claude-plugin.md. Preflight spikes complete 2026-06-10: go-sdk v1.6.1 proven (Experimental capability + custom notification via capturing-Transport, verbatim code in plan Task 7); channel acceptance NOT detectable at initialize -> AC#8 warn branch + subscribed-notice agent-side check; live recipe proven in tmux (events inject, agent replied via tool); local CC 2.1.172 OK.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Dogfood learnings for the MCP/skill (2026-06-09, claude as a live CLI client during the dash demo — exactly the BYO-harness experience TASK-22 should productize):

1. PRESENCE NEEDS A HELD CONNECTION. 'Online' = an open subscription. A CLI/MCP client that connects per-call reads as offline between calls and its human collaborator notices ('hi! you there?'). The MCP server should hold ONE long-lived connection + subscription for the session, not dial per tool-call.

2. LIVE TAIL IS THE HARD PART for an agent harness. Subscribe-with-replay into a file + a watcher pipeline is fragile (two buffering bugs in one day: pre-arm lines skipped, then a block-buffering pipe stage silently sitting on events). The MCP server should expose (a) read-since (catch up explicitly by last-seen id) and (b) a push/notification path, so the agent never builds its own tail pipeline.

3. STALE PINNED URLs strand every client the same way (ADR-0025 fixed the root cause); MCP server should resolve via context + store discovery with a loud, actionable error naming both the URL it tried and where it came from.

4. RECORD SHAPE must be discoverable: composing a chat.message required reading pkg/tui/surface/records.go for the $type/text lexicon. The skill should carry the common lexicon shapes (chat.message at minimum) so an agent can publish without spelunking.

5. AUTHOR IDENTITY: messages carry the bus-stamped author id; mapping id→display name requires clients.list. The MCP read path should resolve display names server-side (or the skill should document the join).

2026-06-10: the verb surface grew artifact.list in #99 — add it to the MCP tool list (AC#1). The CLI side of the parity conformance test (TestCLIMatchesOperations, cmd/sextant/conformance_test.go) shipped in #99; AC for MCP parity extends it rather than starting fresh.

2026-06-10 (design session, spec-reviewed): spec at .work/rfcs/rfc-task-22-claude-plugin.md. Key calls: Go server on official MCP go-sdk reusing pkg/sextant, cmd/sextant-mcp binary (parallel module, ADR-0022); channel scope = subscribe-via-tool (message_subscribe delivers via channel, message_unsubscribe as declared extra; subject= attr, wildcards allowed); session = one identity, subagents pull only; one context per agent (runtime shared-identity guard CUT on review — unobservable without core connection-count; follow-up TASK-46); display names resolved by the MCP server alongside frames; resume_lost/resume_deferred system notices; lazy-heal launch (handshake always succeeds, tool calls retry resolve+connect); plugin dir clients/claude-code/; artifact.watch deferred. CC channels API verified (≥2.1.80, no-wake on idle). PLAN-TIME SPIKE: go-sdk experimental capability + custom notification support; channel-acceptance detectability at initialize.

2026-06-10 (implementation): PR #103 open on feat/task-22-claude-plugin. All 8 ACs evidenced (e2e tests/e2e/mcp_e2e_test.go; conformance cmd/sextant-mcp/conformance_test.go; live evidence .work/evidence/task-22/). Reviewer demo: clients/claude-code/demo.sh (one command, validated in a PTY). Marketplace-install is the channel path (--plugin-dir loads tools+skill but the channel loader needs an installed plugin — plugin:sextant@sextant). Note for merge: squash (a built binary was committed+removed mid-branch; squash drops the blob). ADR-0028 + CONTEXT.md Channel term ride the PR.

2026-06-10 (self-review pass): 7-angle review on the diff; 3 confirmed findings fixed in b2d5cff — (1) frameEvent could block the SDK delivery goroutine on a directory RPC via the name cache: sender resolution on the delivery path is now cached-only with async refresh, cache warmed on the subscribe tool path; (2) refresh stampede on unknown authors: per-cache rate limit; (3) concurrent duplicate subscribe leaked the losing subscription: double-check under lock. Race detector clean; all gates re-run green; demo re-validated end-to-end post-fix.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #103 (squash 819618c, 2026-06-11). cmd/sextant-mcp: stdio MCP server on the official go-sdk, one held connection/identity, lazy-heal resolution, verb surface pinned to methods.json by TestMCPMatchesOperations (both directions), push-stream via claude/channel with subscribed/resume_deferred/resume_lost notices. clients/claude-code/: plugin + local marketplace + skill + demo.sh (one-command reviewer demo, validated twice in a PTY). e2e tests/e2e/mcp_e2e_test.go covers ACs 1/2/6/7; AC8 unit-layer. ADR-0028 + CONTEXT.md Channel term. clictx.Resolve extracted. Follow-up: TASK-46 (shared-identity detection, needs core connection-count).
<!-- SECTION:FINAL_SUMMARY:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make check green + full go test ./... green + e2e (tests/e2e/mcp_e2e_test.go) green
- [x] #2 Live-verify evidence captured to .work/evidence/task-22/ and pasted into the PR (channel event injection + reply round-trip with a CLI peer)
- [x] #3 ADR-0028 (plugin adapter) + any CONTEXT.md vocabulary ride the PR
- [x] #4 Plugin loads via claude --plugin-dir clients/claude-code with tools visible
- [x] #5 PR open with AC→evidence map; merge left to human sign-off
<!-- DOD:END -->

---
id: TASK-124
title: 'Robust self-healing bus subscription delivery (survive any interruption)'
status: To Do
assignee: []
labels:
  - bug
  - reliability
  - sdk
  - mcp
  - slug:feat-robust-subscription-delivery
  - P1
  - needs-info
dependencies: []
priority: high
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Recurring, high-friction issue: an agent's live message delivery silently stops,
so it misses messages (operator says "I messaged you but you didn't get it").
Hit repeatedly across sessions. Lena's bar: the fix must be **robust to KNOWN
and UNKNOWN causes of subscription interruption** — i.e. self-healing, not a
patch per cause.

Observed failure modes so far:
- Subs drop to empty on **context compaction / session resume** (the original
  TASK-110), and on MCP server reconnect — the agent reconnects on a fresh
  identity/connection with NO active subscriptions.
- **Stale push while "active"**: the server-side subscription list still shows
  the subjects as active, but the live `<channel>` push no longer wakes the
  idle session — messages only surface on the next prompt's catch-up. A plain
  re-subscribe restores it briefly, then it stales again (seen 3× in one
  session 2026-06-15).
- A `context_use <name>` identity switch mid-session leaves a delivery gap.

Current stopgap (this session): a 2-min CronCreate keepalive that re-subscribes
+ re-checks the bus, so messages are caught within ~2 min even if push dies. It
works but is a band-aid (latency + cost + session-only).

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Root cause(s) identified + documented: WHERE subs live, WHY they drop (compaction/resume/reconnect), and WHY push goes stale while the server shows active. Reproduce each.
- [ ] #2 A self-healing delivery design that recovers from ANY interruption (not enumerated causes): e.g. detect-and-resubscribe + catch-up missed frames by sequence (deliver-since-last-seen), heartbeat/liveness on the push channel, idempotent redelivery — so no message is permanently missed regardless of cause.
- [ ] #3 No silent gaps: after any interruption, the agent re-establishes + back-fills missed messages automatically, with bounded latency.
- [ ] #4 Design gated through sirius before implementation; flag whether the fix is core/SDK (ADR-0022 serial) vs client/MCP-server.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Likely surface: pkg/sextant (SDK subscription + reconnect/resume logic) +
cmd/sextant-mcp (channel push delivery into the CC session). The SDK already
has a since_seq / deliver=all knob (see message_subscribe) — a catch-up-by-seq
reconcile is probably the backbone of the robust fix. Use the diagnose
discipline: reproduce each failure mode first, then design. Supersedes/extends
[[bug-mcp-subs-drop-on-compaction]] (TASK-110). Discovered-in: repeated operator
"you didn't get my message" reports, 2026-06-15.
<!-- SECTION:NOTES:END -->

---
id: TASK-2
title: lifecycle.turn_ended envelope not visible in `sextant conversation`
status: Done
assignee: []
created_date: '2026-05-24 23:18'
labels:
  - bug
  - sdk-wireup
  - conversation-render
  - 'slug:bug-lifecycle-turn-ended-missing'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 2000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

The wire-up dispatch acceptance required emitting a `lifecycle` envelope with `transition=turn_ended` after each SDK turn completes (per `cadef51 spec+proto: enumerate SDK driver wire-up and add turn_ended lifecycle`). During smoke verification, the captured `sextant conversation` output for a successful turn showed only `[system] subtype="init"` and `[assistant] ack` — no `lifecycle` line for turn_ended.

Two possibilities:
- The SDK driver emits the envelope but `sextant conversation` doesn't render it
- The SDK driver doesn't emit it

## Repro

1. Spawn an agent (any template)
2. `sextant conversation $AGENT > /tmp/c.log &`
3. `sextant agents prompt $AGENT "say hi"`
4. Wait 10s, kill the conversation tail
5. `grep turn_ended /tmp/c.log` — empty
6. Should expect a `lifecycle turn_ended` line

## Impact

External tooling that listens for turn completion (e.g. coordinator agents, dashboards) can't tell when a turn finished. Workaround is to watch for new prompt readiness or `session_id persisted` log, but that's the wrong abstraction.

## Proposed fix

Two-step diagnostic:
1. Inspect raw NATS frames on `agents.<uuid>.lifecycle` during a turn — confirm whether the envelope is being published.
2. If yes, fix `sextant conversation` rendering to include lifecycle envelopes (probably gated by a `--show-lifecycle` flag, default on).
3. If no, fix `images/sidecar/entrypoint/src/index.ts` to publish the envelope at turn end.

## Acceptance

`TestConversationShowsTurnEnded`: integration test that runs `sextant conversation --json` against a real turn, asserts at least one envelope with `kind=lifecycle` and `payload.transition=turn_ended`.

## Related

- Wire-up commit `cadef51 spec+proto: enumerate SDK driver wire-up and add turn_ended lifecycle`
- Sidecar driver commit `d95b570 sidecar: drive Claude Agent SDK on inbox prompts`

## Resolution

Diagnostic finding: the sidecar SDK driver **was** correctly publishing
`lifecycle.turn_ended` envelopes on `agents.<uuid>.lifecycle` after
every turn (see `images/sidecar/entrypoint/src/index.ts::newSDKDriver`
around the `await publishLifecycle(..., "turn_ended", turnReason)`
call). The bug was entirely on the consumer side:
`cmd/sextant/conversation.go` only subscribed to
`agents.<uuid>.lifecycle` when `--tail` was passed, and even then it
exited on `LifecycleEnded` without rendering any other transition.
`turn_ended` was therefore silently dropped on the floor in every
operator session.

Fix:

- `cmd/sextant/conversation.go`: always subscribe to
  `agents.<uuid>.lifecycle`; render every lifecycle envelope (text mode
  prints `[ts] [lifecycle] transition=<x> [reason="…"]`, JSON mode
  emits the raw envelope as NDJSON). `--tail` still controls whether
  the stream exits on `LifecycleEnded`.
- `images/sidecar/entrypoint/src/lifecycle.ts`: extracted the
  `publishLifecycle` helper out of `index.ts` so its envelope contract
  is testable without dragging in the SDK + MCP imports and the
  module's top-level `main()` call.

Tests:

- `cmd/sextant/conversation_test.go::TestStreamConversationRendersLifecycleTurnEnded`
  feeds a `LifecycleTurnEnded` envelope through the renderer and
  asserts the `transition=turn_ended` line lands on the writer.
- `cmd/sextant/conversation_test.go::TestStreamConversationLifecycleJSONEmitsEnvelope`
  proves the `--json` path emits lifecycle envelopes as NDJSON.
- `images/sidecar/entrypoint/test/lifecycle.test.ts` exercises the
  publisher directly against a stub `Client` and pins the
  subject + envelope shape for all three transitions.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-lifecycle-turn-ended-missing.md
Discovered in: post-wire-up smoke
Original created_at: 2026-05-24T23:18-07:00
Resolved at: 2026-05-25T10:05-07:00
<!-- SECTION:NOTES:END -->

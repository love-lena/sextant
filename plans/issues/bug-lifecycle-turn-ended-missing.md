---
title: lifecycle.turn_ended envelope not visible in `sextant conversation`
status: open
priority: P3
created_at: 2026-05-24T23:18-07:00
labels: [bug, sdk-wireup, conversation-render]
discovered_in: post-wire-up smoke
---

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

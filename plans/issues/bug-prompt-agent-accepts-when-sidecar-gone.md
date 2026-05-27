---
title: prompt_agent RPC returns ok=true even when the target sidecar is gone
status: open
priority: P1
created_at: 2026-05-26T15:05-07:00
labels: [bug, daemon, rpc, operator-experience]
discovered_in: chat TUI Checkpoint C — sent prompts to an agent whose container had exited; RPC succeeded, no frames ever arrived, `ask` timed out
---

## Summary

`prompt_agent` accepts a prompt and returns `ok=true` without verifying that the target sidecar is actually reachable. The handler publishes the prompt onto the sidecar's NATS subject and trusts that someone is subscribed. When the sidecar has crashed / exited / been removed (and `[[bug-agents-list-stale-lifecycle]]` masks this), the prompt is dropped silently and the caller's only signal is a `turn_ended` lifecycle envelope that never arrives.

Concrete repro (from the discovery session):

```
$ sextant ask 2b5fcfe4-… "ping" --timeout 10s
sextant: ask: timeout waiting for turn_ended lifecycle (waited 10s)
EXIT=2

$ docker ps | grep 2b5fcfe4
# (nothing — the sidecar container is gone)
```

The RPC returned `ok=true`. The sidecar wasn't there. The prompt vanished.

## Why P1

This is the canonical "fail loudly at the boundary" violation. The CLI looks broken (timeout with no useful error), the daemon looks fine (the RPC succeeded), and the operator has no idea where the prompt went. Combined with `[[bug-agents-list-stale-lifecycle]]`, it produces "agent is running per the listing but my prompts disappear into the void" — one of the most operator-hostile failure modes possible.

## Fix shape

The `prompt_agent` handler should verify sidecar liveness before publishing:

1. Cheap check: is the sidecar's heartbeat fresh? Heartbeats already flow on `agents.<uuid>.heartbeat` per `pkg/sextantproto.HeartbeatPayload`. The store could cache the last heartbeat timestamp and reject `prompt_agent` if it's older than, say, 30s.
2. Direct check: query the sidecar via a synchronous RPC (e.g., `sidecar_ping`) before publishing the prompt — adds latency on the happy path.
3. Lifecycle check: refuse `prompt_agent` if the agent's lifecycle is in a terminal state (`ended` / `crashed` / `archived`).

Prefer (3) for the immediate fix, then (1) for the deeper safety net.

Return a structured error so the CLI can surface a useful message:

```json
{"ok": false, "error": "agent_not_reachable", "reason": "lifecycle=ended; restart with `sextant agents restart <uuid>`"}
```

## Acceptance

- `TestPromptAgentRejectsEndedAgent` — sidecar publishes `transition=ended`, store records it, subsequent `prompt_agent` returns `ok=false` with `reason` field containing `"ended"` or `"restart"`.
- `TestPromptAgentRejectsStaleHeartbeat` — sidecar heartbeat is older than threshold; `prompt_agent` returns `ok=false`.
- `sextant ask <uuid> "ping"` on a dead agent exits with a clear "agent is ended, run `sextant agents restart <uuid>`" message, not a generic timeout.

## Related

- `[[bug-agents-list-stale-lifecycle]]` — upstream cause; the lifecycle field has to be fresh for option (3) above to work.
- `[[feat-ask-conversation-self-diagnose-on-timeout]]` — CLI-side mitigation; less ideal than fixing the RPC.
- `[[feat-chat-tui-status-dot]]` — UI signal that prevents the operator from sending in the first place.

---
title: Sidecar NATS client doesn't reconnect after DISCONNECT — agent goes deaf to the bus
status: open
priority: P1
created_at: 2026-05-26T17:30-07:00
labels: [bug, sidecar, nats, resilience, network]
discovered_in: chat TUI Checkpoint C — laptop slept, came back, assistant agent stopped responding; daemon and container still healthy
---

## Summary

When the sidecar's NATS client loses its connection to the daemon (e.g. laptop sleep, network blip, NATS server restart), it does not reconnect. The container and Node process stay alive, but the sidecar can no longer:

- Receive new prompts (`agents.<uuid>.inbox` subscription is dead)
- Publish frames or lifecycle envelopes
- Send heartbeats

Concrete repro from the discovery session:

```
$ docker logs sextant-assistant-3d26e88a --tail 5
... "inbox: prompt queued" streamSeq:62 (last successful inbox event at 23:18:36Z)
... "heartbeat publish failed","err":"DISCONNECT" at 00:16:46Z
```

The `heartbeat publish failed: DISCONNECT` line is the smoking gun — the sidecar tried to publish, found the connection dead, logged the error, and… did nothing else. No retry, no reconnect, no exit. The agent is silently deaf.

Symptoms from the operator's side:
- `sextant agents show` reports `lifecycle: running` (the daemon's record was last updated when the sidecar was still healthy)
- `sextant ask <uuid>` returns `timeout waiting for turn_ended lifecycle`
- `docker ps` shows the container is up
- `docker exec <container> ps -ef` shows the sidecar Node process is alive

Reproduces deterministically by sleeping the laptop with an active agent and waking it later. Almost certainly also reproduces by restarting the daemon process (which restarts the embedded NATS server) while sidecars are running.

## Why P1

Same severity as `[[bug-agents-list-stale-lifecycle]]` and `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — agents look healthy but are non-functional. This particular failure mode is **operator-induced by routine actions** (laptop sleep), making it disproportionately common.

The current root-cause tickets cover "sidecar is gone" (container exited) and "lifecycle field is stale". This issue is a third sibling: "sidecar process alive but bus connection dead". The fixes don't fully overlap — heartbeat-staleness checks ([[bug-prompt-agent-accepts-when-sidecar-gone]] fix shape) would surface this, but the right root-cause fix is making the sidecar's NATS client robust to disconnects.

## Likely root cause

The `nats.go` client supports auto-reconnect via `nats.MaxReconnects(-1)` + `nats.ReconnectWait(...)` options, but it must be opted into. Inspect `sidecar/src/index.ts` (or wherever the NATS connect call lives) — the connect options likely don't enable infinite reconnect, or the reconnect handlers are missing.

Even with auto-reconnect, JetStream consumers behind the scenes may need re-establishment on reconnect. Verify that JetStream subscriptions automatically recover, or wire explicit re-subscription on the `reconnect` event.

## Fix shape

1. **Enable infinite reconnect on the sidecar's NATS client.** Set `maxReconnectAttempts: -1`, a reasonable `reconnectTimeWait`, and a randomized backoff jitter.
2. **Re-subscribe JetStream consumers on reconnect.** If the inbox / heartbeat / outbound subscriptions don't auto-recover, attach a `reconnect` event handler that re-creates them.
3. **Exit on permanent failure.** If reconnect is genuinely impossible (e.g. config error), the sidecar should exit non-zero so the daemon's supervisor can spawn a fresh incarnation. Hanging silently is the worst failure mode.
4. **Log reconnect events as `warn` not `info`** — operators investigating downtime need them visible.
5. **Heartbeat staleness side check** ([[bug-prompt-agent-accepts-when-sidecar-gone]] connection) — even with reconnect, the heartbeat KV staleness check would catch any remaining gap.

Optional follow-on: the daemon could detect "this agent's heartbeat hasn't moved in N minutes" and proactively restart the sidecar. Avoids requiring the operator to notice and run `agents restart`.

## Acceptance

- `TestSidecarReconnectsAfterDaemonRestart` — integration test: spawn agent, restart daemon (kill + relaunch the NATS server), within 30s the sidecar publishes a heartbeat indicating it reconnected.
- `TestSidecarExitsOnUnrecoverableNATSFailure` — sidecar configured with a bad URL exits non-zero rather than hanging.
- Manual: sleep laptop with active agent, wake, send a prompt — agent responds within a few seconds without operator intervention.

## Related

- `[[bug-agents-list-stale-lifecycle]]` — daemon-side view of agent state that doesn't reflect sidecar reality. Even with this fix, the daemon's record will still go stale until a separate fix lands.
- `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — `prompt_agent` should refuse on stale-heartbeat agents; that check would currently catch the symptom of this bug, but doesn't fix the underlying connection loss.
- `[[feat-sextant-agents-check]]` — would surface this state ("heartbeat last seen 1h ago, container alive, lifecycle says running ⚠") in the diagnostic chain.
- Memory of prior daemon-restart work (M5 "TestDaemonRestartsNATSAfterKill") — proved the daemon side reconnects; the sidecar's symmetric resilience apparently isn't validated.

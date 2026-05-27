# Operator-experience conventions — sextant

Sextant's value depends on operators being able to drive it
confidently. This doc captures the patterns that keep the operator
surface honest. Anything in here exists because we hit the failure
mode at least once.

For CLI formatting / exit-code rules see
`conventions/tui-conventions.md` §"CLI conventions". This doc is about
*what makes operator-facing surfaces useful*, not how to format them.

## Fail loudly at the boundary

If an RPC, subscribe, or publish succeeds at the bus layer but the
work never reaches a worker, return an error — not `ok=true`. The
"silent success" failure mode is the most operator-hostile pattern
sextant has historically shipped.

Concretely:

- Check downstream liveness before accepting a request. If the target
  sidecar's last heartbeat is stale or its lifecycle is terminal,
  refuse the RPC with a structured error that names the remedy.
- When a publish goes to a subject with no subscribers and the caller
  expects a response, that's an error, not a noop.
- Surface "channel closed before completion" with the specific
  upstream cause where possible — `ctx.Err()`, "lifecycle=ended",
  "sidecar disconnect", not a generic timeout.

## State must reflect reality

Daemon-side records of agent state should track the lifecycle stream
continuously, not get cached at registration time. The drift between
"what the daemon thinks" and "what's actually running" is the root
cause of nearly every operator confusion we've debugged.

Watch for this pattern:

- `<thing> show` reports `running` but the thing is dead.
- A diagnostic stream (lifecycle, heartbeat, container ps) tells the
  truth; the cached record lies.

Fix: subscribe the record-keeper to the diagnostic stream so the
record stays fresh. Treat the record as a projection of the stream,
not an independent source of truth.

## Self-serve diagnostic commands

Operators should never need to chain `docker ps | grep` + `sextant
tail` + `sextant logs` + `sextant ask --timeout` mentally to diagnose
a stuck agent. Each recurring debug pattern that takes more than two
commands deserves its own sextant verb that produces a verdict +
remedy:

```
$ sextant agents check assistant
agent:     assistant (2b5fcfe4-…)
container: NOT RUNNING                                                ⚠
verdict:   sidecar exited; restart needed
remedy:    sextant agents restart assistant
```

The bar: a verdict line and a copy-pasteable remedy. Don't make the
operator interpret raw signals.

## Resilience scenarios to test

These are the standard operator-induced failure modes. Every
long-running component should survive them without intervention:

- **Laptop sleep / wake.** TCP connections die silently. Auto-reconnect
  with backoff is mandatory for any NATS-connected component, on both
  the daemon side and the sidecar side.
- **Daemon restart while agents are running.** Sidecars must reconnect
  to the new NATS instance without intervention. (Validated for the
  daemon side in `TestDaemonRestartsNATSAfterKill`; symmetric sidecar
  coverage is currently a gap — see `[[bug-sidecar-nats-disconnect-no-reconnect]]`.)
- **Network blip mid-RPC.** RPCs should have bounded timeouts and
  surface a useful error on expiry, not hang.
- **Backlog after reconnect.** When a durable queue drains after a
  disconnect window, surface the drain to the operator (banner, log
  line, "replaying N queued events"). Silent catch-up reads as
  "responding to nothing in particular."

If a component fails any of these, it's a bug, not a config nuance.

## Distinguish failure modes — don't merge them

"Agent appears running but isn't responding" has at least three
distinct root causes seen in this codebase:

1. **Container gone** — process exited, daemon's lifecycle record
   wasn't updated, listing still shows `running`.
2. **NATS disconnect** — process alive, bus connection dead. Common
   after laptop sleep.
3. **Inbox backlog drain** — process alive, bus connected, sidecar
   draining queued prompts from a prior disconnect so new prompts
   look stuck.

Each has a different fix. File three tickets, cross-link them. Resist
the urge to file one umbrella ticket — the fix shapes don't overlap
and a unified fix won't materialize.

## Error messages: structured remedies

Every error the operator sees should be answerable by a copy-pasteable
next step. Compare:

- ❌ `ask: timeout waiting for turn_ended lifecycle (waited 10s)`
- ✅ `ask: agent has lifecycle=ended (since 2026-05-26T00:14Z).
      Restart with: sextant agents restart 2b5fcfe4-…`

When the diagnostic context is available at the error site, surface
it inline. When it isn't, the error should at least point at the
diagnostic command that would provide it.

## Defer-list audit before merge

Before merging any feature with a "Deferred (post-MVP)" section in
the spec, re-scan that section with fresh eyes and ask:

> If this isn't here, what does the operator have to do to recover
> from the failure mode this would have prevented?

If the answer is "run a diagnostic command they may not know about"
or "infer state from the absence of activity", the item isn't actually
optional polish — it's load-bearing UX that should ship in the MVP.

(Concrete example: the chat-tui spec deferred the lifecycle status
dot in the header. The first operator session hit a dead-agent state
and spent ~5 commands diagnosing what the dot would have shown in
red on open.)

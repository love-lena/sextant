---
status: proposed
date: 2026-06-16
---

# Presence and liveness derive from a client heartbeat

[ADR-0020](0020-clients-are-bus-issued-identities.md) made presence a read-time
derivation: `clients.list` reads the live connection table (NATS `Connz`) and
stamps each identity online or offline. It deliberately added no heartbeat —
"the transport's own liveness reports even an ungraceful drop, so presence is
self-correcting within the ping window" — and left a richer signal to TASK-20.

That connection-table view is first-hand and correct **on a single bus**. It has
two gaps the multi-machine work surfaces:

- **It cannot see across a leaf link.** Once a remote box runs a leaf node
  ([TASK-125]), a client connected to the leaf is in the *leaf's* connection
  table, not the hub's — so the hub derives it offline though it is fully
  participating over the federated link. The TASK-125 spike confirmed this is the
  sole blocker to leaf presence.
- **There is no active client-liveness signal.** A wedged client, or a push path
  that has gone silent while the connection looks alive, is invisible to a
  connection-table read.

This ADR records the decision (TASK-126, realizing the long-deferred TASK-20):
**a periodic client heartbeat is the source of presence and liveness.**

## The decision

A connected client sends a periodic `clients.heartbeat` call. The bus does two
things and returns its stamped time:

- **Presence floor.** It stamps a bus-clock `last_seen` on the client's
  `sx_clients` registry record (the same clock that stamps `IssuedAt`). `last_seen`
  is the **leaf-correct** presence source: it is recorded from a federated wire
  call, so it survives a leaf link the connection table is blind to. Presence
  becomes **dual-source** — `online = connection-table-shows-it OR last_seen-is-fresh`
  — so a client behind a leaf reads online to its peers while it keeps beating,
  and a single-bus client still reads online from the connection table the instant
  it connects (no heartbeat-warmup gap).
- **Push-path floor.** It core-NATS publishes the beat back to the client on a
  dedicated, transient subject `sx.hb.<id>`. The client subscribes its own and
  confirms the echo arrives; a beat sent but not echoed within the window is a
  stale push path. This is the per-client liveness floor a watchdog ([TASK-124])
  consumes — distinct from, and composing with, TASK-124's per-subscription
  seq-gap, which catches a single stale subscription on an otherwise-live client.
  The two are independent signals: per-client path-alive (this) and per-sub
  delivery-alive (TASK-124).

## Why these shapes

- **`clients.heartbeat` is a reference-bus operation, not a protocol one.** It is
  not in `methods.json`, has no CLI/MCP surface, and adds no frame shape — the
  wire epoch is unchanged. Presence/liveness is reference-implementation
  behaviour ([ADR-0022](0022-parallel-modules.md)); a fork may omit it. A bus that
  does not implement it answers "unknown operation", and the SDK treats that as
  benign — it stops beating and presence rests on the connection table — never
  crashing. This keeps the change additive and backward-compatible.
- **The echo rides a dedicated transient subject, not the inbox or a delivery
  relay.** `sx.hb.<id>` is a plain core publish: no JetStream persistence and no
  attest-cursor scan (which `msg.client.<id>` would incur), so a 15s beat never
  accumulates in any stream. A missed echo therefore signals a dead path rather
  than queuing a backlog.
- **`last_seen` write is last-writer-wins.** The registry bucket keeps one
  revision per client and a beat only ever advances the time, so the write needs
  no compare-and-set loop. It does not change the operator-verdict review block or
  any other field.

## Consequences

- Presence works across a leaf link and any multi-node topology — the foundation
  the cross-machine / offline work ([TASK-125]) builds on.
- It realizes TASK-20's read-time-liveness intent and retires the interim
  keepalive that stood in for it.
- Tunables: beat interval (~15s) and freshness window (~45s, ~3× the interval)
  are configurable; `last_seen` stays on the `clients.list` reply as a non-sensitive
  input a consumer may render ("last seen 30s ago").
- A pre-heartbeat client (older SDK) keeps working: it never beats, so its
  presence rests on the connection table exactly as before.

[TASK-125]: leaf-node topology (the presence-across-a-leaf blocker).
[TASK-124]: self-healing subscriptions / the liveness watchdog that consumes the
echo. [TASK-20]: the originally-deferred read-time-liveness heartbeat.

---
status: proposed
date: 2026-06-10
---

# Subscriptions survive a bus restart

A `Subscribe` call returns a `Subscription` that works across a reconnect —
a bus restart of the same store, or a plain network blip with the bus still
up. The SDK re-establishes the server-side relay on reconnect, resuming from
the last delivered sequence so no messages are missed or duplicated. When
re-establishment is impossible — the store was wiped, history expired — the
SDK calls the subscriber's error handler immediately: never silence. When a
resume attempt merely could not reach the bus, the subscription defers —
announced through the same handler, kept registered, retried on the next
reconnect.

## What this guarantees

**A subscription outlives a reconnect.** When the bus restarts against the
same store (same address, same JetStream data — ADR-0025), a live `Subscribe`
subscription keeps delivering without any action from the caller. Messages
published after the restart arrive on the existing handler; messages published
before the restart and already delivered are not re-delivered. The same holds
for a reconnect to a surviving bus (a network blip): the SDK replaces the
bus-side relay it can no longer trust — stopping the old one first, then
re-subscribing — and the resume recovers any messages the old relay published
into the void while the client was disconnected.

**Resume is exact and gap-free.** The SDK records the stream sequence of each
delivered message. On reconnect, it re-subscribes with `since_seq = last + 1`,
so the server relay starts delivering from the first message the subscriber has
not yet seen. A subscription with no deliveries before the restart resumes from
its original start option (`DeliverAll` → from sequence 1; live-only → new
messages only).

**Impossible resume is loud, never silent.** If the resume sequence is no
longer addressable — beyond the stream head because the store was wiped, or
below the first retained sequence because retention expired or purged the
history in between — the SDK calls the `OnError` handler registered at
subscribe time. The subscription is stopped. A subscriber that registered no
`OnError` gets a log line; one that did gets the error. Either way, the caller
is not left watching a subscription that delivers nothing while believing it is
live. The `busfeed` layer always registers `OnError` and routes it to an
`ErrMsg` through the pump, so a Bubble Tea surface shows the failure.

One accepted risk: a store that is wiped and then republished past the old
sequence resumes at a wrong position undetected — the resume sequence is within
bounds again, but it indexes a different history. Detecting that would require
tracking stream identity across restarts, which is deliberately not built.

**A transport-failed resume defers, then retries.** Loud death is reserved for
resumes the bus *answered* with "impossible". When the resume attempt itself
cannot reach the bus — a request timeout, a second blip inside the window —
the subscription stays registered and the next reconnect pass retries it. The
deferral is still announced: `OnError` receives a non-fatal notice wrapping the
exported `ErrResumeDeferred` sentinel (`errors.Is`-distinguishable; any
`OnError` that does not wrap it is fatal). The `busfeed` layer surfaces it as a
non-terminal `ResumeDeferredMsg` — the pump stays alive and the notice clears
on the next delivery. One accepted gap for now: the retry rides the reconnect
cadence, so a deferral on a connection that stays healthy waits for the next
reconnect (a bounded retry timer is tracked as TASK-40,
[[bug-sdk-resume-deferral-no-retry-cadence]]).

**`Stop` during downtime is clean.** Stopping a subscription while the bus is
unreachable calls `subscription.stop` on the bus (which will time out or
succeed once the bus is back), but the subscription is marked stopped
immediately. When the client reconnects, the resume pass skips already-stopped
subscriptions. No goroutine leak, no panic, no spurious error delivery.

## Why

ADR-0025 made the bus's address stable across restarts of the same store, so a
reconnected NATS connection reaches the right bus. That closed the transport
gap: the connection survives. Before this ADR, the read side did not match: the
JetStream consumer backing a `Subscribe` was ephemeral and tied to the old
server process. The connection came back but the delivery stream did not, leaving
a subscriber in a permanently silent state — the bug that prompted this record.

The busfeed package's doc comment already promised that "the SDK already
reconnects"; this ADR makes that promise true for the subscribe side as well.

## Implementation

The SDK's `ReconnectHandler` snapshots the subscription registry and hands the
resume pass to a dedicated goroutine, so the NATS callback dispatcher stays
free for later connection events while each rotation keeps its full
per-subscription deadline. The pass carries the connection's reconnect count
as its token: at most one pass runs per token, a newer token supersedes a
running pass at its next subscription boundary, and a per-subscription lock
serializes any rotations that overlap across passes. "Reconnected to the bus"
logs at the end of a completed, non-superseded pass — only once every relay
that reconnect owed is live again (or has deferred or failed loudly) — so
callers waiting on the log see a settled bus, and `Close` drains an in-flight
pass with a bounded wait. Each active
`subscription` stores the last delivered stream sequence (an atomic `uint64`
updated on every quarantine-passing delivery). On reconnect, `reestablish`
replaces the relay generation wholesale: it stops the old relay on the bus
(`subscription.stop` — idempotent, so it is a no-op after a real restart, and
it clears the surviving relay after a plain blip), then subscribes under a
fresh sub-id — and with it a fresh private delivery subject — and sends a
fresh `message.subscribe` Wire API call for that sub-id carrying
`since_seq = last + 1` (or the original start policy when `last = 0`). The
rotation makes every frame attributable to exactly one relay: anything a
replaced relay still has in flight lands on a delivery subject the live
generation never subscribes. Each generation's delivery handler is also
stamped with the connection's reconnect count at establishment and processes
frames only while that count is current, so the cutover holds without timing
assumptions; the monotonic delivery cursor (the last delivered sequence only
moves forward) is a further defense layer, dropping any non-increasing
sequence as overlap. The bus relay handles `since_seq` by mapping it to a
`StartFromSeq` backend start with a stream-bounds check: a `since_seq` beyond
the stream's last sequence plus one, or below its first retained sequence,
returns `backend.ErrSequenceGone`, and the bus surfaces it as a call error,
which the SDK turns into an `OnError` call and a subscription stop. The SDK
classifies resume failures by *who* failed: an error the bus replied with
arrives as a typed call error and is fatal as above; an error where the bus
never answered (timeout, closed connection) is the transport's, so the
subscription stays registered and `OnError` gets the non-fatal
`ErrResumeDeferred` notice instead.

Active subscriptions register themselves on the client at creation and
deregister on teardown. The registry (`Client.subs`) is guarded by a mutex; a
resume pass snapshots it under the lock and then re-establishes each relay
without holding the lock, so delivery goroutines and Stop can run concurrently.

## Addendum (2026-06-18, v0.5.2): the subscription also follows a moved address

The guarantee above leaned on ADR-0025 keeping the bus at the *same* address
across a restart. That stability is best-effort: the bus reuses its previous
port only when that port is free at boot, and otherwise binds a new one — so a
restart can come back on a different port (it did, in the v0.5.1 live-bus
incident: a messy service restart left the old port briefly held, the bus fell
back to a fresh port, and every live client was stranded). NATS auto-reconnect
only ever redials the address it booted with and never re-reads discovery, so on
a moved address the connection — and with it every subscription — never recovers.

This addendum extends the guarantee to a moved address. When the connection's
URL was resolved from the discovery file (`bus.json`, via `Options.ConnInfoPath`,
the normal case), the SDK re-reads that file on **every** redial and dials the
address recorded there, overriding the stale boot address NATS would otherwise
reuse. Reconnect therefore follows the bus to its new port, and the resume
machinery described above runs unchanged on the new connection — so a moved
address self-heals with no client restart and no operator action, exactly as a
same-address restart already did. A caller that pinned an explicit
`Options.URL` is dialed as-is (its choice is deliberate and respected); if the
discovery file is unreadable or unparseable at dial time the dialer falls back
to the address NATS chose, so a transient read never makes a reconnect worse
than before.

One accepted bound: this follows a moved **port** on the same loopback host —
the case that occurs today. A moved **host** under TLS would additionally need
the verify-host re-resolved (TLS hostname verification keys off the server-pool
name, not the dialed address); that is deferred with the leaf-TLS follow-up
(ADR-0038) and not built, since the current bus is a single plaintext loopback.

Implemented as a NATS `CustomDialer` (`reResolveDialer` in
`pkg/sextant/reconnect.go`), attached in `Connect` only when the URL came from
discovery. The regression that locks it (`TestSubscriptionFollowsBusPortChange`)
is deliberately prod-faithful: it drives the real SDK connect path via discovery,
restarts a real bus on a genuinely different port, rewrites `bus.json` as the bus
does on boot, and asserts the subscription reconnects and keeps delivering — a
port-pinning fake would pass while production broke.

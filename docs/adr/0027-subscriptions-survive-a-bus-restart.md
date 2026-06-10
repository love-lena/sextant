---
status: proposed
date: 2026-06-10
---

# Subscriptions survive a bus restart

A `Subscribe` call returns a `Subscription` that works across a bus restart of
the same store. The SDK re-establishes the server-side relay on reconnect,
resuming from the last delivered sequence so no messages are missed or
duplicated. When re-establishment is impossible — the store was wiped, history
expired — the SDK calls the subscriber's error handler immediately: never
silence.

## What this guarantees

**A subscription outlives a bus restart.** When the bus restarts against the
same store (same address, same JetStream data — ADR-0025), a live `Subscribe`
subscription keeps delivering without any action from the caller. Messages
published after the restart arrive on the existing handler; messages published
before the restart and already delivered are not re-delivered.

**Resume is exact and gap-free.** The SDK records the stream sequence of each
delivered message. On reconnect, it re-subscribes with `since_seq = last + 1`,
so the server relay starts delivering from the first message the subscriber has
not yet seen. A subscription with no deliveries before the restart resumes from
its original start option (`DeliverAll` → from sequence 1; live-only → new
messages only).

**Impossible resume is loud, never silent.** If the resume sequence is beyond
the stream head — because the store was wiped or retention expired the relevant
history — the SDK calls the `OnError` handler registered at subscribe time. The
subscription is stopped. A subscriber that registered no `OnError` gets a log
line; one that did gets the error. Either way, the caller is not left watching a
subscription that delivers nothing while believing it is live. The `busfeed`
layer always registers `OnError` and routes it to an `ErrMsg` through the pump,
so a Bubble Tea surface shows the failure.

**`Stop` during downtime is clean.** Stopping a subscription while the bus is
unreachable calls `subscription.stop` on the bus (which will time out or
succeed once the bus is back), but the subscription is marked stopped
immediately. When the client reconnects, `reestablishSubs` skips already-stopped
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

The SDK's `ReconnectHandler` calls `reestablishSubs` before logging "reconnected
to the bus", so the log fires only after all relays are live. Each active
`subscription` stores the last delivered stream sequence (an atomic `uint64`
updated on every quarantine-passing delivery). On reconnect, `reestablish`
issues a fresh `message.subscribe` Wire API call carrying `since_seq = last + 1`
(or the original start policy when `last = 0`). The bus relay handles `since_seq`
by mapping it to a `StartFromSeq` backend start with a stream-bounds check: if
`since_seq` exceeds the stream's current last sequence plus one, the backend
returns `backend.ErrSequenceGone` and the bus surfaces it as a call error, which
the SDK turns into an `OnError` call and a subscription stop.

Active subscriptions register themselves on the client at creation and
deregister on teardown. The registry (`Client.subs`) is guarded by a mutex;
`reestablishSubs` snapshots it under the lock and then re-establishes each relay
without holding the lock, so delivery goroutines and Stop can run concurrently.

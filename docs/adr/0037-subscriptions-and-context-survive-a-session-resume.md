---
status: proposed
date: 2026-06-16
---

# Subscriptions and the active context survive a session resume

A harness session's bus-following state should outlive the process that holds
it. The plugin adapter ([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md))
keeps a session's manual subscriptions in an in-memory map and its `context_use`
choice in a field. Both are lost the moment the session resumes on a fresh
process — a `--resume`/`--continue`, a context compaction, or an MCP restart. So
after a resume a `message_subscribe` silently stops delivering, and a session
that had switched identity with `context_use` quietly reverts to its auto-mint
id. The agent looks connected and never learns it stopped hearing the subjects
it was following.

The session's **inbox already survives** all of this: it is auto-subscribed on
every connect ([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md),
TASK-55) and the trust hook re-reads it from a durable per-session cursor
([ADR-0029](0029-a-harness-speaks-as-itself.md)/[ADR-0030](0030-clients-act-on-a-principals-messages-as-operator-input.md)),
so a principal's DM still wakes a resumed session. The asymmetry — the inbox
heals, the manual subscriptions and the context do not — is the whole defect
(TASK-124). The fix is to give the rest of a session's bus-following state the
same durability the inbox already has.

## What persists

The adapter records, per session, under the writable plugin-data dir keyed on
the stable `CLAUDE_CODE_SESSION_ID` — the same place and key as the attest
cursor:

- the **active context** (the `context_use` choice), and
- each **manually-subscribed subject** with the **last stream sequence**
  delivered on it.

The auto-inbox is *not* tracked here: it re-establishes itself on every connect
already, and the SDK re-subscribes it across an in-process reconnect
([ADR-0027](0027-subscriptions-survive-a-bus-restart.md)). Only the manual
subscriptions — the ones with no other path back — are persisted. The per-frame
write is debounced (the sequence cursor flushes on a short timer and at
shutdown); a subscribe/unsubscribe/switch writes through immediately, so the set
of subjects and the chosen identity survive even a hard kill.

## How it heals

On every connect the adapter restores from that record:

1. **Re-pin the context** before the auto-mint fallback, so a resumed session
   reconnects as the identity it switched to — not a fresh stranger. An explicit
   `--creds`/`$SEXTANT_CONTEXT` still wins ([ADR-0029](0029-a-harness-speaks-as-itself.md)
   precedence is unchanged); the persisted context only fills the slot that used
   to fall through to a new mint.
2. **Re-subscribe each subject and catch it up.** It subscribes live first, then
   reads the frames missed since that subject's last delivered sequence
   (`message_read` from the cursor) and delivers them as channel events. Live
   first, then backfill, so the union has no gap; the small overlap is dropped by
   frame id. A long offline gap is bounded — past a cap the session is told to
   read the remainder itself rather than be flooded.

The agent does nothing: it neither re-subscribes nor re-runs `context_use` after
a resume. Its subjects keep arriving and it keeps speaking as the same identity.

## Why this needs no new protocol

The bus already retains frames, and a live subscription already resumes across a
*bus* restart by sequence ([ADR-0027](0027-subscriptions-survive-a-bus-restart.md)).
The only thing missing was the adapter's own state across a fresh *process*. The
catch-up rides the existing `message_read` cursor; the restore rides the existing
`message_subscribe`; the last-seq comes from the sequence already stamped on each
delivered frame. So this is an adapter convention over primitives that already
exist — no new wire operation, the protocol epoch is unchanged, and the core is
untouched. It also retires the interim keepalive cron that had been papering over
the drop.

## Liveness composes on top (a following slice)

Persist-and-restore closes the resume drops — a fresh process, a reverted
identity. A *silent push-stall while the process is still alive* (a subscription
that stops delivering without an error) is closed by a per-subscription
**sequence-gap** check that composes with the client heartbeat
([ADR-0036](0036-presence-and-liveness-derive-from-a-client-heartbeat.md)): the
heartbeat is the per-client floor (no echo coming back ⇒ the whole push path is
dead ⇒ restore everything), and the sequence gap is the per-subject stall check
(a hole in the delivered sequence ⇒ catch that subject up). Two independent
signals feeding the one catch-up path above. That watchdog ships as its own
change once the heartbeat has landed; this ADR records the durable foundation it
builds on.

## A known bound

One narrow case is **not** closed by persist-and-restore alone: a `deliver="new"`
subscription that has never delivered a frame (an idle topic) and resumes *before*
its first frame. Its cursor is still 0, so the restore re-opens it live-only —
frames published in the dead window are older than the new live relay and are not
back-filled. Closing it precisely needs the **stream tail at subscribe time** (to
catch up from the subscribe point without replaying pre-subscribe history), which
the bus does not expose to the adapter; a read from the start of history would
instead flood the agent with backlog it explicitly skipped by choosing `new`. A
subscription that has delivered *any* frame is primed and resumes losslessly, so
this bound is the never-yet-delivered idle case only. The **liveness slice**
above closes it: its per-subject sequence-gap check (already core-touching, so it
can carry the small stream-tail getter) catches the gap the cursor couldn't.

A second, even narrower bound is the **restore-vs-discard race**. Restore runs
asynchronously; a generation check makes it bail if the client is discarded
(reconnect / `context_use`) between subjects. But a discard landing *during* a
single subject's `subscribe` call can leave one stale entry bound to the
now-closed client, which the replacement client's restore then skips as "already
active" — so that one subscription stays dead until the next lifecycle event. It
is bounded and **self-healing**: the next reconnect/resume clears the live map and
rebinds, and the liveness sequence-gap check detects the dead subscription
meanwhile. Fully eliminating it needs per-generation subscription identity (a
compare-and-swap on the live map); that machinery is deliberately not worth it for
a window this narrow, and it is tracked as a follow-up.

## Consequences

- A resumed session self-heals: manual subscriptions keep delivering and the
  chosen identity persists, with no agent action and no operator intervention.
- Skills stop telling agents to re-subscribe or re-`context_use` on resume.
- The interim keepalive cron is no longer needed.
- Durability is per-session: it engages only with both a writable plugin-data dir
  and a stable session id (otherwise the adapter runs in-memory, never sharing a
  durable file across unrelated sessions).
- The bright lines hold: the adapter heals **its own** delivery and manages
  nothing on the bus (signal + cooperate, never track + manage); it is a
  convention over existing primitives (content stays opaque); the thin core does
  not change.

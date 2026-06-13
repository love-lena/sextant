---
status: proposed
date: 2026-06-12
---

# A dispatcher mints its own workers (mint-on-behalf)

A dispatcher stands up agents on demand: it receives a `spawn.request`, launches
a child process, and the child joins the bus as its own actor. For the child to
be a first-class citizen — a **dispatcher-known**, **resume-stable**, **named**
identity the dispatcher can ack and supervise — the dispatcher needs to mint that
identity. This ADR lets a dispatcher mint its workers **with its own bus
identity**, so automation never has to hold the operator's credential.

The bus stays the sole minter ([ADR-0020](0020-clients-are-bus-issued-identities.md)):
the signing keys never leave it, and a dispatcher only *asks* it to mint. What is
new is who the bus authorizes on the issuance path.

## The fence is inverted: deny spawned workers, not an allowlist

The first cut gated minting behind a blessed `kind=dispatcher` — an allowlist. We
inverted it, because **kind is self-declared and only weakly enforced**, so making
it load-bearing for a security boundary is fragile. Instead:

> **Any registered client may mint children — except a spawned worker.**

The marker is a bus-stamped field, not a kind. When a client mints on-behalf, the
bus records `SpawnedBy = <minting client's id>` on the child's durable registry
record ([ADR-0020](0020-clients-are-bus-issued-identities.md)). The mint
authorization then reads one thing: does the *caller's* record have a `SpawnedBy`?
If it does, it is a spawned worker and may not dispatch; if not, it is a top-level
client and may. Operator- and enrollment-minted identities have no `SpawnedBy`, so
they are top-level by construction.

Because the marker is set by the bus at issuance — never supplied in the call —
the boundary does not depend on kind being meaningful, and it cannot be
self-asserted. The same field doubles as the spawn lineage in the registry.

This is more flexible than the allowlist (any client can act as a dispatcher with
no special registration) while keeping the one guarantee that matters.

## The guarantee: a worker cannot recursively dispatch

The one thing the fence protects is that **a spawned worker cannot stand up
children of its own**. That bounds a spawn tree to the top-level clients the
operator actually brought onto the bus: a compromised or runaway worker cannot
fork-bomb the bus with descendants, because every identity it might try to mint is
refused.

Recursion still works — it just flows through a dispatcher. A spawned agent that
wants a child publishes a `spawn.request`; the (top-level, non-spawned) dispatcher
honours it and mints. So the request chain can nest arbitrarily, while minting
authority never descends into the workers themselves. (Note the two lineages this
produces: a child's `spawn.ack` names the *requester* as `parent` — the chain of
intent — while its registry `SpawnedBy` names the *minting dispatcher* — the chain
of authority. They coincide unless a worker requested a spawn another dispatcher
fulfilled.)

This composes with [auto-mint](0029-a-harness-speaks-as-itself.md): auto-mint lets
*any* harness JOIN the bus under a per-session identity with no setup; mint-on-behalf
is for the dispatcher that needs a child's id **up front** (to ack and track it),
**stable** across the child's resumes, and **named** instead of the `claude-<hex>`
auto-mint placeholder.

## Blast radius

The change is one branch in `clients.register`'s authorization plus a one-field
read of the caller's record (fail-closed: an unreadable record may not dispatch);
the mint path, the per-client allow-list, and the unforgeable author are unchanged.
The boundary is deliberately permissive — any top-level client may mint agents (a
resource lever the operator already has) — and the protection is narrow and
precise: descendants cannot mint. Bounding a dispatcher's spawn rate is a future
operator-policy concern, not an authority one.

This is the lone locked-core change in M5.2 ([ADR-0022](0022-modules-over-a-locked-core.md));
the dispatcher and supervisor that consume it are client-side modules. It refines
the issuance picture of [ADR-0020](0020-clients-are-bus-issued-identities.md) and
realizes the spawn lifecycle of [ADR-0009](0009-spawn.md).

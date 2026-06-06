---
status: accepted
signed_off_by: lena
date: 2026-06-05
---

# Clients are bus-issued identities

This refines [ADR-0008](0008-clients-are-processes.md) (a client is a process),
[ADR-0012](0012-reserved-namespace-and-authn.md) (per-client authn), and the
identity half of [ADR-0019](0019-implementing-the-bus.md). 0008 framed a client as
a process — one process, one identity, gone when the process exits. We keep its
isolation rule and its "build a supervisor as a client" stance and refine the one
equation at its center: **a client is a durable identity the bus issues; a process
is how that identity connects.**

**A client is a durable, bus-issued identity — one key, whole lifecycle.** The
credential *is* the client: exactly one key per client, issued once, valid across
disconnect and reconnect. The identity outlives any single process — a harness can
register, do some work, send a message, exit, and come back later under the same
id — so a client has a lifecycle the bus tracks (issued → … → retired), independent
of whether a process is attached right now. "Client" stops being a fuzzy label that
many keys can wear and becomes one concrete, single-keyed thing.

**The bus is the sole minter — you ask for an identity; you cannot mint your own.**
A client obtains its identity by asking the bus, which mints and records it. This
is strictly stronger than `sextant token`, which signed a credential *offline* from
the account key on disk — making "can create an identity" mean "can read the keys."
The guarantee here is **key custody**: the signing keys live inside the bus and
nothing else holds them, so the bus is the single source of truth for who exists
and can record, scope, and later revoke every identity. The CLI becomes a thin
client that asks the bus and needs no key access of its own.

**One issuance path, and it is the single exception to "you must already be
someone."** Every operation the bus serves requires the caller to *already hold* a
bus-issued identity — except one: `clients.register`, the path that *issues* an
identity. It has to be the exception, because you cannot demand an identity in
order to obtain your first one. So `clients.register` accepts two ways of
authorizing the request:

- **a held identity** — an already-authenticated issuer (the operator, or later a
  managed-auth parent): "mint one I will hand to someone else";
- **a bootstrap authorization that requires no pre-existing identity** (this mode
  is *enrollment* — getting your first identity) — ranging from locality at the
  weak end (a process on the same machine) to a signed enrollment token at the
  stronger end (which works remotely): "mint one for me to use."

In both modes the bus does the *same thing* — mint a new identity and return its
credential. The auth mode is purely the authorization gate (and, at the connection
layer, how an identity-less caller is admitted at all); it is not a different
operation. A human operator at a terminal calls `clients.register` with the
operator credential `sextant up` provisioned; a Claude Code session that boots on
the same box and is *not* the operator reaches the same path authorized by locality
and is minted a plain client identity. Both are "ask the bus for an identity."

**Why this path need not be an authenticated call.** The rest of the Wire API is
authenticated and identity-scoped (`sx.api.<id>.>`, allow-listed to your own id)
for one reason: the **unforgeable author** — the author the bus stamps on a message
or artifact cannot be forged because you may publish only under your own id. That
guarantee is about *authorship of records*. Identity *creation* is not governed by
it; its guarantee is key custody (above). So `clients.register` does not
intrinsically need authentication-as-a-client — it needs *authorization* (who may
mint, and on what basis), and a held identity is only one way to satisfy that. This
is what lets the one path accept bootstrap trust without weakening anything that
matters: the author stays unforgeable because the bus remains the sole minter.

**Presence is derived from the connection, not asserted by a call.** Whether a
client is connected *right now* is computed by the bus from the live connection,
not declared by the client. Because the bus is the embedded server, it knows this
first-hand — from connection-lifecycle events and its own connection table — so
online ≡ "an authenticated connection for this id exists." Nothing heartbeats; the
transport's own liveness (idle pings) reports even an ungraceful drop, so presence
is self-correcting within the ping window (a clean close is immediate; a
crash/partition resolves a ping interval or two later). Reconnecting with the same
key flips the same identity back online.

**Three distinct lifecycle events, not two.** *Register* (the one issuance path
above) mints the identity, once. *Disconnect* drops presence to offline — at any
time, with no notice needed — and is not an end of life. *Retire* (formerly
`deregister`) decommissions the identity for good: a deliberate end, not "I'm
leaving for now," and an authenticated call like any other. A clean `Close`
therefore goes offline; it does **not** retire.

**Consequences.** The clients registry becomes a **durable store of issued
identities** the bus persists — it survives a bus restart and recognizes a
reconnecting client by its authenticated key — each carrying a derived presence
status; `clients.list` is the join of the two: every registered client and whether
it is online now. Read-time liveness (TASK-20) stops being "reap stale ghosts" and
becomes "keep the presence status current" — the stale-entry problem dissolves,
because a disconnected client is legitimately *offline*, not a ghost. `sextant
token` retires into `clients.register`: there is no separate offline mint, because
the keys never leave the bus. The ordinary registry operations a *connected* client
invokes are `clients.list` (read) and `retire`; `register` stands apart as the
issuance path with its own authorization, not an ordinary verb on the authenticated
surface.

**What it sets up.** A client registering *another* client — A asks the bus to mint
credentials for a B that does not exist yet, B stays silent until it stands up and
connects, A retires B when done — is just `clients.register` called in the
held-identity mode by an A that is authorized to mint for others. That ownership
and authorization layer is **managed auth**, deferred to [ADR-0009](0009-spawn.md)
(spawn); this decision only ensures the one issuance path already has the seam for
it. (Issuance returns secret material — the new client's key — so the reply rides
the caller's own connection; fine for the operator-local and managed cases.)

**Status of the implementation.** The M2 cutover (the #76–#85 stack) landed the
call-transport half of the protocol; this ADR is the **identity half**. The stack
still reflects the *prior* identity model — `token` mints offline,
`clients.register`/`deregister` are authenticated calls a client makes about
*itself*, and the registry is presence-only — and this decision revises that: the
single issuance path and its two auth modes, the durable identity store,
connection-derived presence, `token`→`register`, `deregister`→`retire`. It also
updates the identity half of ADR-0019 and the connect-handshake notes in
`protocol/nats-binding.md`. **This is required to finish M2, not a later
follow-up: the cutover stack and this identity model ship together as one
milestone — the cutover is not released on its own.**

Map (ADR-0003): the bus and the SDK (identity + authn), and the clients registry.

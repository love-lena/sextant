---
status: proposed
date: 2026-06-05
---

# Clients are bus-issued identities

This refines [ADR-0008](0008-clients-are-processes.md) (a client is a process),
[ADR-0012](0012-reserved-namespace-and-authn.md) (per-client authn), and the
identity half of [ADR-0019](0019-implementing-the-bus.md). 0008 framed a client
as a process — one process, one identity, gone when the process exits. We keep
its isolation rule and its "build a supervisor as a client" stance and refine the
one equation at its center: **a client is a durable identity the bus issues; a
process is how that identity connects.**

**A client is a durable, bus-issued identity — one key, whole lifecycle.** The
credential *is* the client: exactly one key per client, issued once, valid across
disconnect and reconnect. The identity outlives any single process — a harness
can register, do some work, send a message, exit, and come back later under the
same id — so a client has a lifecycle the bus tracks (issued → … → retired),
independent of whether a process is attached right now. "Client" stops being a
fuzzy label that many keys can wear and becomes one concrete, single-keyed thing.

**Identity is issued by the bus — you ask for one; you cannot mint your own.** A
client obtains its identity by asking the trusted bus, which mints and records it.
This is strictly stronger than `sextant token`, which signed a credential
*offline* from the account key on disk — making "can create an identity" mean
"can read the keys." Routing issuance through the bus keeps the signing key inside
the bus, makes the bus the sole issuer and the single source of truth for who
exists, and lets it record, scope, and later revoke every identity. The CLI
becomes a thin client that asks the bus and needs no key access of its own. This
is what makes the stamped author unforgeable *at the source*: an identity cannot
exist unless the bus made it.

**The first contact is an enrollment step.** Asking the bus for an identity has a
bootstrap: the authenticated Wire API needs an identity to make a call, but the
call is what issues the identity. So enrollment is a distinct, credential-less
path — a request the bus answers with a freshly minted credential — gated by some
bootstrap trust (a shared enrollment secret, or local/operator trust on the bus
host; NATS auth-callout is the full-grown form). The single-host MVP can keep this
trivial: the operator enrolls a client where the bus runs. This enrollment path
is the one genuinely new mechanism the decision introduces, and it is where
managed auth (below) later plugs in.

**Presence is derived from the connection, not asserted by a call.** Whether a
client is connected *right now* is computed by the bus from the live connection,
not declared by the client. Because the bus is the embedded server, it knows this
first-hand — from connection-lifecycle events and its own connection table — so
online ≡ "an authenticated connection for this id exists." Nothing heartbeats; the
transport's own liveness (idle pings) reports even an ungraceful drop, so presence
is self-correcting within the ping window (a clean close is immediate, a
crash/partition resolves a ping interval or two later). Reconnecting with the same
key flips the same identity back online.

**Three distinct lifecycle events, not two.** *Register* (the enrollment act)
issues the identity, once. *Disconnect* drops presence to offline — at any time,
with no notice needed — and is not an end of life. *Retire* (formerly
`deregister`) decommissions the identity for good: a deliberate end, not "I'm
leaving for now." A clean `Close` therefore goes offline; it does **not** retire.

**Consequences.** The clients registry becomes a **durable store of issued
identities** the bus persists — it survives a bus restart and recognizes a
reconnecting client by its authenticated key — each carrying a derived presence
status; `clients.list` is the join of the two: every registered client and whether
it is online now. Read-time liveness (TASK-20) stops being "reap stale ghosts" and
becomes "keep the presence status current" — the stale-entry problem dissolves,
because a disconnected client is legitimately *offline*, not a ghost. `sextant
token` retires into `register`; the offline credential-minting it did survives
only as a provisioning mode — mint-and-hand-off a creds file for a client you do
not run locally — if and when remote provisioning is in scope.

This also settles the question that opened this thread — whether the authenticated
`clients.register` call belongs in `methods.json`. It does not, because that call
goes away: registering is now *enrollment* (its own credential-less path) and
presence is *derived*, so a connected client never makes a register call. The
registry operations a connected client invokes reduce to `clients.list` (read) and
`retire`; enrollment is its own documented step, not a verb on the authenticated
surface.

**What it sets up.** A client registering *another* client — A asks the bus for
credentials for a B that does not exist yet, B stays silent until it stands up and
connects, A retires B when done — is the natural shape of **managed auth**. That
ownership/policy layer is deferred to [ADR-0009](0009-spawn.md) (spawn); this
decision only ensures the identity model has the seams for it.

**Status of the implementation.** The M2 cutover (the #76–#85 stack) implements
the *prior* model: `token` mints offline, `clients.register`/`deregister` are
client calls, and the registry is presence-only. This ADR is the next step.
Adopting it revises that identity and registry surface in a follow-up — and
updates the identity half of ADR-0019 and the connect-handshake notes in
`protocol/nats-binding.md` accordingly. Nothing here blocks merging the cutover
as it stands.

Map (ADR-0003): the bus and the SDK (identity + authn), and the clients registry.

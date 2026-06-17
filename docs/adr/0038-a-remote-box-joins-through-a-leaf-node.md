---
status: accepted
date: 2026-06-16
---

# A remote box joins the bus through a leaf node

[ADR-0019](0019-implementing-the-bus.md) made the bus a single embedded NATS
server: one operator, one SEXTANT account, JetStream for the durable engine
(messages, artifacts, the `sx_*` KV), and a per-client allow-list credential so a
client publishes only under its own `sx.api.<id>` call space. Every client reaches
the bus the same way — request/reply over its wire-API subjects.
[ADR-0021](0021-saved-client-contexts.md)/TASK-24 let a *remote* client reach that
bus over a tunnel: zero code, ideal for a one-off connection.

A box that hosts **many** long-lived agents — a remote orchestration node — wants
more than a per-process tunnel: a persistent local bus those agents share, with
live local-first pub/sub and an automatic re-link when the hub blips. This ADR
records the decision (TASK-125): **a remote box joins by running a local bus in
leaf mode that federates the per-client wire-API subjects to the hub, while the
engine stays at the hub.**

## The decision

A leaf is a second embedded server, started from the same code with leaf flags
set, that links to the hub and federates exactly the per-client subjects within
the one SEXTANT account.

**Hub side.** `sextant up --leaf-listen <host:port>` opens a leaf listener
(default off — unset, the bus is byte-for-byte what it was). The hub mints a
SEXTANT-user **link credential** for the remote leaf, reusing the same per-client
mint path the rest of the bus uses — the link is just another verified SEXTANT
identity, not a privileged tier. Alongside it the hub writes a **public trust
bundle**: the encoded operator, SEXTANT-account, and system-account JWTs and the
two account public keys. The operator carries both to the remote box — the bundle
is public (safe in the clear), the link credential is secret (owner-only).

**Leaf side.** `sextant up --leaf-remote nats-leaf://hub:PORT --leaf-bundle B
--leaf-creds C` runs the bus as a leaf:

- **JetStream is off.** The engine — the messages stream, the artifacts bucket,
  every `sx_*` KV — lives only on the hub, reached over the federated wire API.
- **It installs the hub's public trust, no seeds.** From the bundle it trusts the
  hub operator, resolves the SEXTANT + system accounts, and so authenticates the
  link and every per-client credential (all signed by the hub account) and
  **enforces the same per-client perms locally**. It holds no signing seed, so it
  has no material to mint with.
- **It exposes a loopback (`127.0.0.1`) client listener.** Local agents connect
  here with their hub-minted credentials — the TASK-24 credential flow unchanged.

**What federates.** Exactly the per-client subjects, within the one SEXTANT
account: `sx.api.<id>.>` (calls), `sx.deliver.<id>.>` (push delivery),
`_INBOX.<id>.>` (call replies), and `sx.hb.<id>` (the heartbeat echo). A local
agent's call reaches the hub's wire-API handler, which reads the author from the
subject token and stamps it; the reply, delivery, and echo federate back. No SDK
change, no wire change, no epoch bump — the agent cannot tell it is behind a leaf.

## Why these shapes

- **The leaf can authenticate and enforce, but cannot mint — by key custody.**
  The load-bearing claim for an *honest* leaf is that the hub's *subject-derived
  author stamp* is trustworthy for a leaf client. It holds because the leaf
  enforces the per-client allow-list at its own edge: an agent publishing under a
  foreign id (`sx.api.<other>.publish`) is rejected at the leaf with a permissions
  violation, before any federation. The leaf can do this with public account JWTs
  alone. It cannot mint, because minting needs the account *seed*, which never
  leaves the hub. So issuance stays the hub's sole act, and for an honest leaf the
  author the hub reads off the subject is the identity the leaf already verified.
- **The link credential is scoped to the federation set, not an operator key.**
  The hub-minted leaf-link credential carries the four federated spaces
  (`sx.api.>`, `sx.deliver.>`, `_INBOX.>`, `sx.hb.>`) — it must, to forward every
  agent's traffic — but it explicitly *denies* the two reserved issuance prefixes
  (`sx.api.operator.>`, `sx.api.enroll.>`). So even a leaf box that connects its
  link credential straight to the hub can forward client calls yet can never itself
  ask the bus to mint, retire, or claim the principal. Possession of the link
  credential is not possession of an operator key.
- **The engine stays at the hub.** JetStream off on the leaf is the same bright
  line as "the engine is a library in a client, never in the core": the durable
  store and its ordering are one authority, at the hub. The leaf is transport and
  local enforcement, not a second source of state. (Per-node JetStream for offline
  replication is a later, separate decision built on this foundation, not this
  one.)
- **The link rides a secure transport; the listener fails closed off loopback.**
  The leaf link has no TLS today, so it MUST ride a secure transport — SSH
  reverse-tunnel, Tailscale, or WireGuard — and the leaf listener binds loopback
  behind that transport. This is enforced, not merely advised: `--leaf-listen`
  *refuses to start* on any non-loopback (or all-interfaces) host, because a
  routable unencrypted leaf listener is the single unacceptable configuration. A
  loopback bind is allowed bare on purpose — the external secure transport carries
  the encryption. Native leaf-listener TLS is a follow-up productization ticket
  that would lift the loopback-only constraint; until then the bus will not open a
  listener it cannot keep private.
- **Presence needs no new machinery — the heartbeat already crosses the leaf.**
  A leaf agent is in the *leaf's* connection table, not the hub's, so a
  connection-table read alone would show it offline. The merged heartbeat
  ([ADR-0036](0036-presence-and-liveness-derive-from-a-client-heartbeat.md))
  resolves this by construction: the agent's `clients.heartbeat` federates to the
  hub, which stamps `last_seen` on the hub registry, and the dual-source rule
  (`online = connection-table OR last_seen-is-fresh`) then reports the leaf agent
  online. The convergence the heartbeat ADR named is realized here: the same
  signal that gives active client liveness also gives leaf presence, with no
  topology-coupled Connz aggregation.
- **Additive and default-off.** All four flags are unset by default, and a bus
  with none of them set is unchanged. The leaf path is a distinct, thinner start
  (no JetStream bootstrap, no operation serving, no minting) so the hub path it
  branches from carries no leaf conditionals in its hot path.

## The leaf box is a trust boundary

The author the hub stamps comes from the call subject, and the per-client scoping
that makes that honest is enforced at the *leaf's* edge — on each agent's own
credential — not on the link. The link must forward every agent's `sx.api.<id>`
calls, so it cannot itself re-check which id an honest agent is allowed to use.
This is inherent to leaf federation: the link is a trusted conduit, and the leaf
box that runs it is therefore part of the trust boundary.

**What the boundary fully closes — the reserved operator/enroll surface.** The
`operator` and `enroll` identities are *hub-local*: no client ever connects to them
over a leaf, so the link has no legitimate reason to touch their subjects. The link
credential therefore **denies their whole surface, in both directions** — pub and
sub on the call space (`sx.api.operator/enroll.>`), the reply inbox
(`_INBOX.operator/enroll.>`), and the push-delivery space
(`sx.deliver.operator/enroll.>`). So a compromised leaf **cannot escalate to
issuance** (it can neither make an issuance call nor intercept an issuance reply's
freshly-minted credential) and **cannot eavesdrop operator state** (it cannot read
the operator's `principal.watch` / `artifact.watch` deliveries). The link touches no
operator/enroll subject at all.

**The accepted residual — the inherent trusted-box eavesdrop and forge on NORMAL
federated clients.** What remains is by necessity, not oversight:

- **A compromised leaf can forge any existing client's author.** Its link credential
  can forward a call subject for any id, and the hub stamps that id as the author —
  there is no second, hub-side check that the forwarding leaf was entitled to that
  id, because by design the leaf is the entity that enforced it. (It can only
  impersonate *existing* clients — it cannot mint new ones, per the closed surface
  above.)
- **A compromised leaf can read the call replies and push deliveries of the clients
  it federates.** The link must subscribe `_INBOX.>` and `sx.deliver.>` to carry
  every federated client's replies and deliveries back across the link, so it can
  read them — for the ordinary (non-infrastructure) clients behind the leaf. This
  cannot be denied without breaking the topology: the link cannot forward what it is
  not allowed to subscribe.

Both residuals are bounded the same way: to the *normal* clients a given leaf
federates, on a box the operator trusts. They do not reach the operator/enroll
surface, which is fully denied.

This is acceptable, and it is precisely why a leaf is for a **trusted remote
orchestration node**, not an arbitrary client. The whole point of leaf mode is to
extend the operator's own fleet onto a box the operator controls, reached over a
secure transport on that operator's network — the same trust class as the hub
machine itself. An untrusted party is given a per-client credential and a tunnel
(the TASK-24 path), never a leaf. So the boundary lands exactly where the operator
already places trust: on the boxes they run. The mitigations that keep it there are
the ones above — the link is scoped (the reserved operator/enroll surface is fully
denied), the listener is loopback-behind-a-secure-transport (no routable plaintext),
and the leaf cannot mint (issuance stays at the hub).

## Consequences

- A remote box can host many long-lived agents on a shared local bus that
  federates to the hub, with identity preserved end-to-end and per-client perms
  enforced at the leaf — proven in an in-process two-bus test (federation +
  identity, leaf-local rejection, presence-via-heartbeat, echo federation, the
  leaf's no-mint/no-drain custody, the link credential's scope to the federation
  set with issuance denied, and the trust-boundary author-from-subject behavior).
- The leaf and the TASK-24 tunnel coexist: the tunnel stays the zero-code path for
  a one-off remote client; the leaf is the choice for a node hosting many agents
  (shared local bus, local-first latency, auto re-link). The leaf link can ride
  the same SSH-R / Tailscale transport the tunnel uses.
- Cross-host honesty rests on hub-stamped values: ULIDs and `bus_time` are stamped
  at the hub, so an honest message from a clock-skewed leaf is not quarantined;
  NTP is advisable, not load-bearing.
- This is the foundation the offline / per-node-replication slice builds on; that
  slice (per-node JetStream + owner-only artifacts) is a separate decision.
- The hub mints a fresh link credential on every start (the credentials are
  reserved-name minted material, not durable records — the same model as the
  operator/enrollment creds). So a hub restart invalidates the remote box's copy
  of `leaf-link.creds` and the link will not re-establish until the operator
  re-carries the new file. The `--leaf-listen` start output says so; a durable
  link credential (or a rotation-aware re-link) is a possible later refinement.

Lena signs this at the single v0.5 → main sign-off.

[TASK-125]: leaf-node topology for multi-machine orchestration.
[TASK-24]: the remote-client tunnel this complements.
[TASK-126]: the heartbeat (ADR-0036) that gives leaf presence for free.
Follow-ups: native leaf-listener TLS; per-node JetStream + owner-only artifacts.

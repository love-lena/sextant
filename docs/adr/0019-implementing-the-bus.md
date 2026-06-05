---
status: accepted
signed_off_by: lena
date: 2026-06-04
---

# Implementing the bus: call transport, frame stamping, and identity

ADR-0018 fixed *what* Sextant is — the bus implements the operations over a
pluggable backend, owns access, and stamps the frame. This ADR fixes *how*, in
one pass, so the M2 build (TASK-29/30) is mechanical: the call transport, what
the bus stamps, the identity model, the backend interface, namespace
enforcement, and the SDK's new shape. It consolidates the open-questions tickets
it touches (TASK-8 identity, TASK-9 write-precision, TASK-11 backend contract).

## 1 — Clients reach the bus only through the Wire API; the Wire API rides the connection clients already have

Per ADR-0018, **nothing is direct**: a client never publishes to `msg.*`,
subscribes to `msg.*`, or touches a KV bucket. It makes a **call** — a request to
a reserved Wire-API subject the bus serves, returning the operation's result (a
new id and sequence, the stamped frame, or an error). The bus responder is the
only thing that touches the backend.

The carrier is the authenticated connection the client already holds to the bus
(the embedded NATS, per-client JWT, ADR-0012): the Wire API rides it as
request/reply on reserved `sx.api.*` subjects. **No second listener** — the
existing connection and its auth *are* the transport.

**Verified `author` without trusting the client.** NATS does not tell a responder
who published a message — the forgeable-sender gap ADR-0018 names. We close it
with the permission model already in place: each client's credential permits
publishing only under a subject token carrying its **own** id —
`sx.api.<clientULID>.<operation>` — and the bus reads the author from that token.
Because the server enforces the publish permission, a client cannot call as
anyone else; `author` is bus-trusted without ever being asserted in the record.
Push delivery is symmetric: the bus relays frames to `sx.deliver.<clientULID>.*`,
which that client's credential alone may subscribe to.

**What the credential grants (a change from today).** This requires flipping the
client credential from the current deny-list (ADR-0012, with per-client
write-precision deferred) to a per-client **allow-list**: publish only to
`sx.api.<id>.>`, subscribe only to `sx.deliver.<id>.>` and its own `_INBOX.>`,
with the bus responders using `allow_responses` to reply; **no** direct `msg.*` or
KV access at all. The allow-list is what makes this section's identity claim real
— it is not yet in place (`clientPermissions()` is deny-only today), so TASK-29
must land it.

**Serving the call.** A responder replies only **after** the backend acknowledges
(a publish returns once the log durably holds the frame, not on enqueue).
Responders run concurrently with bounded workers and per-request deadlines, so a
slow backend op cannot head-of-line-block other callers. Push delivery
(subscribe/watch) is **bus-owned**: the bus holds the backend cursor for each live
subscription and relays frames to `sx.deliver.<id>.*`, so replay across a client
reconnect is the bus's responsibility — not core NATS at-most-once delivery — and
the backend keeps no per-reader state (§4).

**Consequence:** `msg.*` and the KV buckets become **bus-internal** — only the bus
reads and writes them. The client-facing surface is `sx.api.*` (calls) +
`sx.deliver.<id>.*` (push). That is what makes the bus the sole access point in
practice, not just in principle — and it lets the bus decide, per request, what a
caller may publish, read, or subscribe to (richer than static NATS perms).

This is the **transport**, kept separate from the **storage interface** (§4): the
operations and the frame are identical regardless of what stores the log. M2 uses
NATS for both, cleanly separated in code. A future non-NATS storage backend keeps
NATS as pure transport (or brings a native listener); either way the protocol —
operations + frame — does not change. That is the ADR-0018 invariant.

## 2 — The bus stamps the frame; the client supplies only the record

In each responder the bus stamps `id` (a fresh **bus-minted ULID** — the trusted
unique id and dedup key), `kind` (message|artifact, set by the operation), `epoch`
(its current protocol epoch), and `author` (the caller's ULID from §1). For
artifacts it also stamps `revision` (from the backend's compare-and-set result)
and `createdAt`/`updatedAt` (the **bus clock** — trusted, ADR-0006). The client
sends only `record`. Anything the bus stamps is **ignored** if a record tries to
carry it — never honoured.

## 3 — Bus-minted ULID is the identity for everything trusted; `display_name` is the human handle

Every serious id is a **bus-minted ULID** — unforgeable, collision-free, owned by
the bus: the `frame.id` of every message and artifact, the client id (minted by
`sextant token`, carried in the credential, so it is exactly the authenticated
identity), and the artifact's id. **Identity and addressing are uniform: all three
entity types — clients, messages, artifacts — are identified *and addressed* by
their ULID.** A **`display_name`** is a non-keying, human-readable **attribute** on
clients and artifacts — never their address: `clients.list` returns ULID +
display_name, and a display_name resolves to a ULID through the registry/index when
a human wants a handle. So `author` is a ULID; direct addressing is
`msg.client.<ULID>`; and artifacts are created, fetched, updated, and deleted by
their ULID `id`.

This consolidates TASK-30 (client identity) and TASK-8. **The decision: ULID is
the address everywhere; `display_name` is an attribute, never a key.** It is the
uniform, purest model — the bus mints and owns every id, and nothing is addressed
by a forgeable or renameable human string — at the cost of a `display_name`→ULID
lookup when a human reaches a thing by name. On acceptance, `methods.json`'s
artifact operations address by `id` (ULID) and `display_name` becomes an optional
attribute set at create.

## 4 — The backend interface is the semantic contract as a small Go interface, shaped to the protocol and checked against Redis

The operation logic is written once against one internal interface; each backend
module satisfies it. The interface is the `semantic-contract.md` primitives
rendered as Go: append-to-log + read-from-cursor + subscribe (a durable, ordered,
replayable log with **no per-subscriber state** — the bus picks the start
position); compare-and-set put + get + watch + key-enumeration (named, versioned
records); the epoch read; and the identity binding the bus stamps `author` from.
Each method is fixed only after answering *"how would Redis satisfy this?"*, and
the answers say where a backend must do **work**, not where NATS leaks: a
**cursor** is a bus-opaque monotonic token each backend *synthesizes* (the NATS
module from the JetStream sequence, a Redis module from the stream id — they are
not assumed equal); **CAS** is expected-revision (`WATCH`/`MULTI` or a Lua check);
**watch** delivers current-value-then-changes, which a backend lacking
value-carrying change events (e.g. Redis keyspace notifications, which are bare
and lossy) must satisfy with a durable change stream + read-repair, not the raw
events. **"No per-subscriber state"** means the *backend* holds none — the **bus**
owns each subscription's start position and cursor (§1). So the seam is shaped by
the protocol, not by JetStream/KV. The NATS module is the first implementation
(notes in `nats-binding.md`); a **conformance suite** the interface must pass is
built alongside it (TASK-28's "one surface" guarantee starts here). This is
TASK-11, now built rather than documented.

## 5 — The bus enforces the namespace and write-precision because it is the sole writer

With clients off the backend, the reserved-namespace guard (ADR-0012) and
per-client write-precision (TASK-9) move from "NATS permissions on client
connections" to "the bus is the only writer." The bus refuses to touch the `sx.*`
reserved space except through the defined operations — a client cannot write
another client's registry row, and `clients.list` is read-only. The verified
identity from §1 is what makes own-row-only enforceable: the bus knows the caller,
so it scopes every write. The client credential still cannot reach `msg.*`/KV
directly (defense in depth), but correctness now lives in the bus.

This section is **design philosophy more than a hard M2 requirement**: the intent
is that correctness lives in the bus, and an idiomatic implementation that leans on
the embedded NATS server's own guarantees is fine — we are building Sextant in a
very NATS way, and that is okay. The hard, security-critical guarantee is the
unforgeable `author` (§1); own-row write-precision (TASK-9) is the direction, not a
gate.

## 6 — The SDK becomes a thin client of the Wire API; Go only for M2

`pkg/sextant` stops driving NATS for the primitives: `Publish` / `Subscribe` /
`FetchMessages` / the artifact methods / `ListClients` each become a Wire-API call
(§1), with the SDK doing only request framing, reply/stream plumbing, and
skew/epoch checks. The SDK is a convenience over the wire, not the protocol. M2
ships the **Go** SDK, CLI, and MCP server; the TypeScript SDK is Future (TASK-5).

**Packaging boundary.** Only the SDK is public. Sextant's own implementation — the
bus, the operation responders, the backend interface and its modules — lives under
Go `internal/` packages; `pkg/sextant` (the SDK) is the single exported module
other programs import. And the modules are **deep**: narrow interfaces over
substantial implementations (the backend interface is the exemplar — one small
surface, a whole NATS module behind it). A broader refactor will follow; for now,
design deep.

## Consequences (applied by TASK-29/30)

- `pkg/wire`: `Envelope`→`Frame`, `sender`→`author`, ULID ids, artifact frame fields.
- `protocol/methods.json`: artifact operations address by `id` (ULID) per §3;
  `display_name` becomes an optional create-time attribute; the call/stamping
  model documented.
- Repo layout: Sextant's implementation moves under `internal/`; `pkg/sextant`
  (the SDK) is the only exported module (per §6), with deep modules behind narrow
  interfaces.
- `protocol/semantic-contract.md`: unchanged in spirit — now *realized* as the Go
  interface.
- `pkg/bus`: gains the operation responders (concurrent, bounded, reply-after-ack),
  frame stamping, the backend interface + NATS module, bus-owned subscription
  cursors, and namespace/write-precision enforcement.
- `pkg/bus/auth.go`: the client credential flips from the current deny-list to a
  per-client **allow-list** (publish `sx.api.<id>.>`; subscribe `sx.deliver.<id>.>`
  + `_INBOX.>`; `allow_responses`; **no** `msg.*`/KV) — the enforcement §1 and §5
  rely on, not yet in place.
- `pkg/sextant`: reframed per §6.
- New reserved subjects: `sx.api.*` (calls) and `sx.deliver.<id>.*` (push);
  `msg.*` and the KV buckets become bus-internal.

## What this is not

Still no control plane (ADR-0018): the bus serves operations, it does not
supervise, reconcile, or spawn. Not a new auth model — it reuses ADR-0012's
per-client JWT, now also as the source of `author`. Not client↔client
request/reply (TASK-23) — that stays parked.

## Why

ADR-0018 said *what*; this fixes *how*, in one pass, so the build is mechanical.
The load-bearing move is reading `author` from a permission-enforced subject
token: it closes the forgeable-sender gap with the auth we already have, so the
bus owns access without a second transport and without trusting the client.

Map (ADR-0003): implements ADR-0018; reuses ADR-0012 (auth) and ADR-0006 (bus
clock); realizes ADR-0013 / `semantic-contract.md` as the backend interface;
folds TASK-8/9/11 and sequences TASK-29/30.

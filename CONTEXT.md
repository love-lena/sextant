---
status: accepted
signed_off_by: lena
date: 2026-06-04
---

# Sextant

The shared language for Sextant — a protocol and SDK for AI agents to
communicate and collaborate over a bus. Definitions only: no architecture, no
implementation. For *why*, see `docs/adr/`; for *how to work here*, see
`AGENTS.md`.

## Language

**Client**:
A process that speaks the Sextant protocol — the universal unit. A harness,
monitor, dispatcher, human UI, or workflow coordinator is each a client.
_Avoid_: participant, service, node, agent (when you mean the process)

**Principal**:
The one human's client, per bus, that is the root of authority — the identity
whose messages other clients act on as their own operator's direct input (the
trust model is ADR-0030). Designated at **bootstrap by the operator and
bus-enforced**: only the bus owner sets or changes it, so the **human-only**
guarantee holds at the source; other clients (including auto-minting agents)
discover and adopt it, and can never claim it. An opinionated **extension** over
the locked core, not a core-protocol concept — the universal protocol stays
principal-free and open among authenticated clients (ADR-0012, ADR-0022).
Distinct from the bus-owner **Operator** tier (ADR-0012/0015) and from a plain
**Client**.
_Avoid_: super user, sudo, owner; operator (that is the bus-owner credential
tier, a different concept)

**SDK**:
The library you build a client with.
_Avoid_: client library

**Bus**:
What clients connect to in order to reach each other and their shared work — the
sole access point, which implements the protocol's operations. It runs on a
pluggable **backend**.
_Avoid_: broker, server, queue, backend (the backend is what the bus runs on)

**Backend**:
The pluggable stream substrate the bus runs on, behind one internal interface;
swappable (a different module per substrate) without changing the protocol.
Opaque to clients.
_Avoid_: naming a specific backend in client-facing docs; "batteries" as the
formal term (only the embedded-convenience framing)

**Message**:
A typed record published on a subject, for events and conversation.
_Avoid_: event (when you mean the frame), packet

**Topic**:
A named subject many clients publish to and subscribe to. A naming
convention over the messages space, not a bus construct — it has no registry,
membership, or access control.
_Avoid_: channel (reserved for the Claude Code harness push mechanism), room,
group

**Artifact**:
A named, versioned unit of durable shared work, owned by one author at a time
(a plan, a review, a result).
_Avoid_: document, file, blob, state

**Record** / **Lexicon**:
The typed content a **frame** carries — user space. Its type is a *lexicon* —
the schema, named on the record itself.
_Avoid_: payload, body

**Frame**:
The bus-stamped wire wrapper around a record — id, kind, epoch, author. The
**record is user space; the frame is bus space** (the bus produces it, not the
client). `kind` discriminates a frame: a **message** in flight, an **artifact**
at rest.
_Avoid_: envelope (renamed), wrapper, header

**Operation**:
A domain action the bus implements and a client invokes — publish, read, the
artifact operations, listing clients. The set of operations is the protocol's
surface.
_Avoid_: verb, method, command, RPC

**Call**:
A client invoking an operation on the bus and receiving its result — the
client↔bus path.
_Avoid_: request/reply (that is client↔client)

**Request / reply**:
A client↔client exchange: one client sends a request to another and gets a reply.
Distinct from a **call** (client↔bus).
_Avoid_: using "request/reply" for the client↔bus call

**Clients registry**:
The durable, bus-maintained directory of issued client identities — written when
the bus issues an identity, removed only by retire; it survives disconnect and bus
restart and lists offline clients too (ADR-0020).
_Avoid_: presence (that is the read-time liveness *view* over the registry, not
the registry itself), service discovery

**Context**:
A saved (bus URL + identity + creds) profile a client install keeps under a
local name, so commands need no connection flags once one is active — the
kubectl/`nats context` pattern. Client-side and local; not a bus construct. Its
name is a handle you choose (at register time it defaults to the display name),
distinct from the identity's bus-minted ULID and its non-unique display name.
_Avoid_: profile, account, session, environment

**Channel**:
The Claude Code harness push mechanism (a research-preview feature): the
plugin adapter (`sextant-mcp`, ADR-0028) declares it and pushes inbound bus
messages into the session as `<channel>` events. A harness construct, never a
bus one — the bus-side concept is the *topic*.
_Avoid_: using "channel" for anything bus-side

**Workflow**:
A multi-step collaboration, driven by a coordinator client.
_Avoid_: pipeline, job, DAG (shapes a workflow may take, not the thing itself);
using "workflow" loosely for any scenario — that is a *use case*, not a Workflow

**Coordinator**:
The client that drives a workflow's steps and records its progress.
_Avoid_: orchestrator, manager, controller

**Dispatcher**:
A client that turns spawn requests into running clients.
_Avoid_: scheduler, supervisor (it launches; it never supervises)

**Epoch**:
The protocol version; a client checks it on connect.
_Avoid_: version, schema version

**Ephemeral** / **Canon**:
*Ephemeral* is the agent workspace (specs, plans), never committed. *Canon* is
the signed-off, committed docs. committed ⇔ signed-off.
_Avoid_: draft, scratch

**Stop** / **Drain**:
A client cooperatively shutting itself down on a signal.
_Avoid_: kill (reserve that for forcing a process from the outside)

## Relationships

- A **bus** carries many **clients'** **messages** and holds their **artifacts**.
- A **client** publishes and subscribes to **messages**, and reads and writes
  **artifacts**.
- A **workflow** is run by a **coordinator**; a **dispatcher** spawns new
  **clients**. Both coordinator and dispatcher are just clients.
- The **clients registry** lists every issued client; **presence** is its liveness
  view — who is connected right now, derived at read time from the bus's live
  connection table.
- A **client** makes a **call** to invoke an **operation** on the **bus**; the bus
  stamps a **frame** around the record and stores or relays it via the **backend**.
- A **bus** has at most one **principal** (a human's client), designated at
  bootstrap by the operator and **bus-enforced**; other clients discover and
  adopt it. It is an opinionated extension over the core — the universal protocol
  has no principal. A client that is not the principal is just a **client**;
  there is no separate role for the trusting side.

## Flagged ambiguities

- "presence" once named the registry itself — resolved: the thing is the
  **clients registry**; "presence" is only its liveness view.
- "participant" drifted against the running process — resolved: the term is
  **client**.
- "client library" vs the process — resolved: the library is the **SDK**; the
  process is a **client**.
- "channel" named both a bus room and the Claude Code harness push mechanism —
  resolved: the bus concept is a **topic**; "channel" is reserved for the
  harness mechanism (ADR-0017).
- "envelope" for the wire wrapper — resolved: it is a **frame** (record = user
  space, frame = bus space).
- "verb" for a protocol action — resolved: an **operation**.
- "face" for the ways to reach the bus (SDK / Wire API) — considered and
  dropped: no collective noun; they are just how a client speaks the protocol.
- "agent" for a participant or a direct-address target — resolved: the universal
  term is **client** (direct addressing names a client, not an "agent").
- "request/reply" used for the client↔bus path — resolved: request/reply is
  **client↔client**; the client↔bus path is a **call** (ADR-0018).

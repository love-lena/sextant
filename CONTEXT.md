---
status: accepted
signed_off_by: lena
date: 2026-06-02
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

**SDK**:
The library you build a client with.
_Avoid_: client library

**Bus**:
The shared substrate clients connect to in order to reach each other.
_Avoid_: broker, server, queue

**Message**:
A typed record published on a subject, for events and conversation.
_Avoid_: event (when you mean the envelope), packet

**Topic**:
A named room on the bus that many clients publish to and subscribe to. A naming
convention over the messages space, not a bus construct — it has no registry,
membership, or access control.
_Avoid_: channel (reserved for the Claude Code harness push mechanism), room,
group

**Artifact**:
A named, versioned unit of durable shared work, owned by one author at a time
(a plan, a review, a result).
_Avoid_: document, file, blob, state

**Record** / **Lexicon**:
The typed content of a message. Its type is a *lexicon* — the schema, named on
the record itself.
_Avoid_: payload, body

**Clients registry**:
The self-maintained directory of which clients are present.
_Avoid_: presence (that is the read-time liveness *view* over the registry, not
the registry itself), service discovery

**Workflow**:
A multi-step collaboration, driven by a coordinator client.
_Avoid_: pipeline, job, DAG (shapes a workflow may take, not the thing itself)

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
- The **clients registry** lists clients; **presence** is its liveness view —
  who is currently alive, judged at read-time from heartbeat freshness.

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

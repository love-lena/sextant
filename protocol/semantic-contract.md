# Semantic contract

The behaviour any backend must honour to host Sextant (ADR-0013 rule 2). It is
the substrate-independent meaning of the verbs in `methods.json`; the NATS
realisation is in `nats-binding.md`. Written so a second-backend author knows
what "correct" means beyond the method signatures.

## Identity (enforced core)

- Every client connects as its own **verified identity**. The `sender` on a
  message is that authenticated identity and **cannot be forged or set by the
  caller** — it is the credential's name, not a field the client supplies.
- A client publishes and registers under exactly the identity it authenticated
  as; what it claims and what the backend authenticated cannot diverge.

## Messages — a durable, ordered, replayable log

- **Durable**: a published message survives until retention expires; it is not
  lost if no one is currently subscribed.
- **Ordered**: messages carry a monotonic **sequence** (the read cursor). A
  reader fetching "since cursor C" sees every message after C, in order.
- **Client-controlled replay**: a consumer chooses where to start — from the
  beginning of retained history (`deliver: all` / `since: 0`) or from now
  (`deliver: new`). The backend keeps **no per-subscriber state**: replay is the
  client's choice, expressed per call.
- **Enveloped**: a message is the `envelope` lexicon wrapping the record. A
  receiver re-validates every consumed message against the envelope schema, the
  protocol epoch, and the bus clock (skew), and **quarantines** (skips) a
  violation rather than delivering it — because the transport permits raw writes
  into the messages space (conventions are optional, ADR-0004).
- **Subjects**: messages are addressed by subject within the messages space.
  Named topics (`msg.topic.<name>`) and direct addressing (`msg.agent.<id>`) are
  conventions over that space, not backend constructs.

## Artifacts — named, versioned, single-author state

- **Named + versioned**: an artifact is a value under a name, with a monotonic
  **revision** that advances on every write.
- **Compare-and-set**: an update succeeds only if the caller's `expected_rev`
  equals the current revision; otherwise it fails. This is the single-author
  discipline — a writer must hold the current revision to change it. Create
  fails if the name already exists.
- **Bare**: an artifact value is the lexicon record itself, **not** enveloped.
- **Watchable**: a watcher receives the current value first, then each change,
  with deletes distinguishable from writes.

## Registry — presence-only directory

- A client writes its own entry on connect and removes it on clean close, keyed
  by its identity. "Listed" means "registered and has not cleanly left." The key
  is the authoritative identity; a record whose body id disagrees with its key
  is corruption, rejected on read and write.
- Liveness beyond "registered" (heartbeat, stale reaping, same-identity
  live-duplicate detection) is deferred (TASK-20).

## Epoch

- The backend publishes the current protocol epoch where clients can read it at
  connect. A client hard-gates on an exact match and refuses to proceed on
  mismatch. Per-message epoch checks quarantine messages from a prior epoch.

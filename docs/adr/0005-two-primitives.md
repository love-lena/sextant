---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# The two primitives

The bus provides exactly two primitives, and only these:

- **Messages** — durable, replayable pub/sub. The *flow* plane: events,
  conversation, requests. Published to subjects and retained in a durable stream,
  so a client that joins late or reconnects can replay — and *the client chooses
  how far back*, so there is no per-client server state.
- **Artifacts** — a versioned KV store. The *state* plane: durable shared work (a
  plan, a review, a result). Named, content-opaque, **single-author**, with
  **compare-and-set** for conflict detection.

Why two, and why these. Messages cover *flow*; artifacts cover *durable state*;
together they are enough that everything else — presence, workflows, spawn,
request/reply — is a **convention** over them, not a new primitive. Holding the
bus to two primitives is what makes it portable (a second backend implements two
things) and legible.

Why single-author artifacts, not multi-writer / CRDT? Agents fork and merge
cheaply, so collaboration is a **lineage of versioned artifacts**
(A writes a plan, B writes a critique, A writes plan v2) rather than concurrent
edits to one object. Single-author + CAS is simpler, more auditable, and needs no
merge engine. Hot history is bounded; long-term audit is a later cold-tier
decision.

Map (ADR-0003): Messages, Artifacts.

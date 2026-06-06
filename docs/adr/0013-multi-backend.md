---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Multi-backend posture

The bus is NATS today, and likely for a long while. But the primitives are
deliberately generic, so we keep the door to a second backend open — at near-zero
cost — with **two rules and nothing more:**

1. **No backend type leaks the public API.** The SDK exposes **domain verbs**
   only — publish, subscribe, put, get, watch — never a NATS message, subject, or
   KV handle. What callers touch is Sextant's vocabulary, not the bus's.
2. **A one-page written semantic contract.** The behavior a backend must honor —
   durability, ordering, compare-and-set, client-controlled replay — written in
   prose, so a future implementer knows what "correct" means beyond the method
   signatures.

**No `Backend` interface yet.** An interface extracted against a single
implementation just encodes NATS's shape under a neutral-looking name; the real
seam only shows itself when a second backend pushes on it. So we **abstract
against a second implementation, not a hypothetical one** — the two rules keep the
option alive cheaply, and the interface gets extracted when something concrete
forces its shape.

Map (ADR-0003): the SDK's public API (domain verbs) and the two primitives (whose
semantic contract any backend must honor); the bus slot itself (NATS today).

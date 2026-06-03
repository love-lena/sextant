---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Vision: what Sextant is

Sextant is a protocol and an SDK — the connective tissue that lets
independently-run AI agents communicate and collaborate over a shared bus.
Agents run wherever their operator runs them and connect to Sextant to find each
other, exchange **messages** (durable and replayable), and share **artifacts**
(versioned, single-author units of durable work). Over those two primitives sit a
few conventions: a **clients** registry, **workflows**, **spawn**, and
**request/response**. Collaboration is a dialogue of messages and a lineage of
versioned artifacts; multi-step work is a workflow driven by an ordinary
coordinator client. The bus runs as one binary (`sextant up`) or as any NATS you
point the SDK at, and the SDK (Go and TypeScript) is how you build *any* client.
**Sextant's value is the split between a small, fixed core and open-ended
extension:** the core is just the protocol, the SDK, and the two primitives —
*everything else is a client you build.* Harnesses, monitors, dispatchers, human
UIs, whole workflows: all clients. Capability grows by adding clients, never by
growing the core. Sextant bundles a number of opinionated reference clients that
can be used directly or forked and extended.

Boundary and trade-off decisions (and why one approach over another) live in
later ADRs.

---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# The bus is NATS, and there is no daemon

The bus is **NATS** (JetStream for the durable stream + KV). You run it one of two
ways:

- **`sextant up`** — a single binary with NATS embedded, bootstrapped with the
  layout. Batteries included.
- **any NATS** — point the SDK at a NATS you already run (local, remote, a
  cluster). Sextant is *client-first*: fundamentally an SDK + a layout that works
  against any NATS; the embedded bus is a convenience, not the definition.

There is **no bespoke daemon and no control plane.** The only long-lived process
is the bus itself. Hooks are client-side; monitors and dispatchers are clients;
liveness is judged at read-time. Sextant never spawns, supervises, restarts, or
reconciles anything.

Why no daemon. Sextant aims to do one thing well and work with the rest of the
rapidly changing ecosystem. A daemon is where features accrete — every "we could
enforce / maintain / coordinate…" lands there — so leaving it out keeps the
surface small: a protocol, an SDK, and a bus.

Why client-first, not embed-only. Embed-only would couple Sextant's releases to
NATS's and fight anyone who already runs NATS. Working against any NATS keeps the
distributed and bring-your-own cases natural, while `sextant up` still offers the
single-binary easy path.

Map (ADR-0003): the bus.

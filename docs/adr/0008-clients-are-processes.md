---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Clients are processes

Every running thing is a **client**: a process that speaks the protocol —
usually via the SDK, though the wire is open to anything that speaks it. A harness, a monitor, a dispatcher, a human-messaging UI, a
workflow coordinator — all the same kind of thing, differing only in what they
do. **Longevity is a behavior, not a type:** some clients run as long as the bus
does, some are spawned for one job and exit; both are just "a process that speaks
the protocol."

**Three ways to run one:**
- **A — process-per-client (the default).** One process = one client = one
  identity = one crash domain. Isolation and language freedom by construction.
- **A2 — single file, no build.** `sextant run reviewer.ts` runs one handler file
  as a client — the low-barrier authoring path: write a handler, no project
  scaffolding.
- **B — dev launcher.** `sextant up --with-dir ./clients/` runs a directory of
  clients as child processes for local development. It launches and forgets — **it
  does not supervise** (no restart-on-crash), which keeps it a launcher.
  Production supervision is your process manager's job (systemd, compose, …).

The line that keeps this simple: **a runner calls functions; it never manages
other processes or identities.** A "host that runs many clients and keeps them
alive" would be a supervisor — shared crash domain, multiple identities in one
process — so instead we keep isolation and one-identity-per-process. A client
that wants many in-process handlers is simply one client with many subscriptions.
The rule binds Sextant, not you: Sextant never manages other processes, but
nothing stops you from building a supervisor *as a client* — coordinating the
others over the bus.

**Two permission planes.** A client has (1) the **local / OS** privileges of
wherever you run it — full by default; sandboxing (a container, a restricted
user) is your choice, not Sextant's — and (2) **bus** access, open among
authenticated clients today, with scoped authz available later (see the
`sx`-namespace ADR).

Map (ADR-0003): Clients, SDK.

---
status: accepted
signed_off_by: lena
date: 2026-06-06
---

# A locked core lets modules build in parallel

The rewrite has landed on `main` (#91), and with it the **core module is
(mostly) locked**: the bus and its protocol contract — the verb/operation surface
([ADR-0017](0017-the-verb-surface-is-the-protocol.md)), the frame
([ADR-0006](0006-wire-atom.md)), the clients-registry record shape with its
connection-derived presence ([ADR-0020](0020-clients-are-bus-issued-identities.md)),
and connection/auth/creds. The conformance test (TASK-28) pins it. *Mostly*,
because a little core-changing work remains — M3-proper (routable bind, TLS, creds
distribution), creds reissue, retention; past that, new work builds *on* the core,
not *in* it.

Everything else is a **module built over the core** — the SDK(s), the clients (the
dash and other TUIs, the orchestration reference clients), the TypeScript SDK. Each
module depends only on the core, not on the other modules, and owns a disjoint set
of packages ([ADR-0003](0003-high-level-architecture.md), the component map). So
the remaining milestones build **in parallel**, not as a strict M3 → M4 → M5
sequence: one worktree per module cut from `main`, with concurrent agents never
colliding — their package sets are disjoint and the core they share doesn't move
under them.

Core changes stay serial. When the core itself must change, that work is
single-owner — one writer on the core at a time — and the modules rebase onto it
when it lands. The core is the one thing every module shares, so it is the one
place we don't parallelize.

Conventions are modules, not core: spawn ([ADR-0009](0009-spawn.md)),
request/response, and workflows ([ADR-0011](0011-workflows.md)) ride the two
primitives ([ADR-0004](0004-conventions-are-optional.md)) and are built as ordinary
client modules, so they parallelize like the rest. The TypeScript SDK is just
another module — parallelizable, low priority, not deferred.

Map ([ADR-0003](0003-high-level-architecture.md)): the core module and the modules
built over it.

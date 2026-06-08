---
status: proposed
date: 2026-06-06
---

# A frozen surface lets the roadmap develop in parallel

The rewrite has landed on `main` (#91): the protocol + SDK + the M2 MVP. The
surface it ships — the verb/operation surface
([ADR-0017](0017-the-verb-surface-is-the-protocol.md)), the frame
([ADR-0006](0006-wire-atom.md)), the clients-registry record shape with its
connection-derived presence ([ADR-0020](0020-clients-are-bus-issued-identities.md)),
and connection/auth/creds — is the **frozen spine**: every track builds against
it, pinned mechanically by the conformance test (TASK-28).

Because the spine is fixed, the remaining milestones run **in parallel, not in a
strict M3 → M4 → M5 sequence**. The dividing rule: a track that only *consumes*
the spine and owns a disjoint set of packages fans out as its own worktree cut
from `main` (the dash in `pkg/tui/...`, the orchestration conventions as a
reference client, the TypeScript SDK under `clients/typescript/`, the M3 spike in
docs). Work that *changes* a spine seam — connection/auth, the frame, the registry
shape (M3-proper, creds reissue, retention) — **serializes: one writer per seam**,
and the fan-out tracks rebase when it lands.

Conventions are not spine: spawn ([ADR-0009](0009-spawn.md)), request/response, and
workflows ([ADR-0011](0011-workflows.md)) ride the two primitives
([ADR-0004](0004-conventions-are-optional.md)), so they fan out as ordinary clients
rather than serialize. The TypeScript SDK is parallel-safe the same way — available
as a low-priority track, not deferred.

Map ([ADR-0003](0003-high-level-architecture.md)): process and sequencing across
the bus, the SDK(s), and the clients.

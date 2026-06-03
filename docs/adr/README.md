# Architecture Decision Records

Decisions are recorded as ADRs: short, numbered, append-only. We supersede
rather than edit. `status: accepted` (with a name in `signed_off_by`) means a
human has signed off — see
[ADR-0002](0002-documentation-and-process-layout.md) for the process.

| #    | Title                                   | Status   |
|------|-----------------------------------------|----------|
| [0001](0001-vision.md) | Vision — what Sextant is  | accepted |
| [0002](0002-documentation-and-process-layout.md) | Documentation & process layout | accepted |
| [0003](0003-high-level-architecture.md) | High-level architecture (the component map) | accepted |
| [0004](0004-conventions-are-optional.md) | Conventions are optional, not core | accepted |
| [0005](0005-two-primitives.md) | The two primitives | accepted |
| [0006](0006-wire-atom.md) | The wire atom | accepted |
| [0007](0007-bus-is-nats-no-daemon.md) | The bus is NATS, and there is no daemon | accepted |
| [0008](0008-clients-are-processes.md) | Clients are processes | accepted |
| [0009](0009-spawn.md) | Spawn | accepted |
| [0010](0010-lifecycle-and-versioning.md) | Lifecycle & versioning | accepted |
| [0011](0011-workflows.md) | Workflows | accepted |
| [0012](0012-reserved-namespace-and-authn.md) | The reserved `sx` namespace, and authn | accepted |
| [0013](0013-multi-backend.md) | Multi-backend posture | accepted |
| [0014](0014-the-tui-is-a-client.md) | The TUI is a client | accepted |

## Review batches
- **Batch 1 — substrate:** 0004–0007 — *accepted*
- **Batch 2 — clients & lifecycle:** 0008–0010 — *accepted*
- **Batch 3 — conventions & cross-cutting:** 0011–0013 — *accepted*
- **0014 — the TUI is a client** — *accepted* (grilled + signed off in-session, 2026-06-02)

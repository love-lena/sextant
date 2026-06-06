---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Workflows

A workflow is a **convention over the two primitives (Messages and Artifacts),
not a primitive of its own** — a peer of the clients registry, built the same way
any client builds its own conventions. *Running* one means running a **coordinator
client**: with no daemon there is no engine in core, so the engine is a **library
inside that coordinator**, never in the substrate.

The primitives already cover the mechanics: a **trigger** is a subscribe, an
**emit** is a publish, **data** is an artifact, **fan-out/join** is `Promise.all`
over request/reply, a **branch** is an `if`. The only missing piece is durable
execution and ergonomics — so the SDK adds a helper plus a **state artifact**:
checkpoint to the artifact, resume at step granularity. (Step-level resume, not
Temporal-grade replay.)

**Two layers, mapping onto the convention-vs-client split:**
- **Layer 0 — the universal foundation.** A workflow is an **`id`** + an
  addressing convention — state in the `sx_workflows` bucket keyed by id, plus
  `sx.workflow.<id>.control` and `sx.workflow.<id>.events` subjects — and a
  self-describing **`kind`**. The state value is otherwise opaque. This is all a
  generic "watch every workflow" tool needs.
- **Layer 1 — `sextant.workflow/v1`, the opinionated reference impl (forkable).**
  A rich shape: status, owner, granular `steps[]`, a dependency graph, a control
  vocabulary (cancel / pause / resume / approve), an SDK helper, and a monitor
  renderer. It is **versioned in its `kind` tag**, so it evolves without an epoch
  bump — and anyone can fork it or ship a different `kind` beside it.

**The DAG is data the coordinator walks; Sextant never executes it.** Control is
**cooperative** — `control` messages ask, they don't compel; force-stopping a
wedged coordinator is the OS's job via its launcher (see lifecycle). The **state
envelope is for observability**: single-writer, CAS-guarded, internally opaque,
with `steps` as a flat status list (no transition logic baked into the
substrate). A free-form **event stream** runs alongside it for history. **Workflow
liveness = owner-in-presence + envelope staleness** — no separate heartbeat.

Map (ADR-0003): Workflow Layer-0 (convention) and the `sextant.workflow/v1`
coordinator (reference client), built on Messages, Artifacts, and the Clients
registry.

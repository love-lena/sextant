# M5.4 workflow coordinator — design notes (TASK-26)

_By canopus, 2026-06-12. Composes the M5.2 dispatcher ([m5-dispatcher-notes.md](m5-dispatcher-notes.md))
into the end-to-end M5 story. Validated on a **throwaway bus**:
`docs/demos/m5-workflow-demo.sh` → **8/8**. Closes the M5 orchestration milestone
(M5.1 spike → M5.2 dispatch → M5.4 workflows)._

## What M5.4 is

A workflow is a **convention over the two primitives, not a primitive of its own**
(ADR-0011): no engine in core. `cmd/sextant-workflow` is an ordinary bus client
that runs the engine as a library and drives a declarative workflow — and a
workflow step **dispatches an agent** through the M5.2 dispatcher, which is what
makes this the full end-to-end demo.

| AC | What it is | Status | Evidence |
|----|-----------|--------|----------|
| #1 | Layer-0 contract: state envelope + control/events addressing | ✅ | state Artifact (CAS) keyed by id + `msg.workflow.<id>.{control,events}`; `cmd/sextant-workflow/records.go`, `records_test.go` |
| #2 | Coordinator walks steps, checkpoints state (CAS), emits events, accepts cooperative control | ✅ | demo: 2-step workflow → both steps + workflow checkpointed `done`; per-step events on the stream; idempotent resume dispatches nothing new; a pre-seeded `cancel` stops it at its safe point |
| #3 | `sextant.workflow/v1` record (status, owner, steps[]) versioned in its kind | ✅ | `protocol/lexicons/sextant.workflow.json` + the `$type` (`sextant.workflow/v1`); demo asserts the envelope shape |
| — | **Composition with M5.2** | ✅ | each `dispatch` step published a `spawn.request`, correlated the dispatcher's `spawn.ack`, and the named agent (`reviewer`, `merger`) joined the bus |

## Layer-0 realized over msg.* + ARTIFACTS (a finding)

ADR-0011 / the M5 breakdown name Layer-0 as *"state in the `sx_workflows` bucket +
`sx.workflow.<id>.{control,events}` subjects."* But the **`sx.` namespace is
reserved for the bus** (ADR-0012): a client publishes only to `msg.*` and writes
only the `ARTIFACTS` bucket — `message.publish` rejects a non-`msg.*` subject, and
there is no Wire op to read/write `sx_workflows`. So those reserved names are **not
reachable by a client today.**

A client-side coordinator (which ADR-0011 mandates — *"an ordinary client," "no
engine in core"*) therefore realizes Layer-0 over the primitives it actually has,
which is exactly ADR-0011's "convention over Messages + Artifacts":

- **state** → a regular Artifact, name `workflow.<id>`, CAS-checkpointed via the
  artifact revision;
- **events** → `msg.workflow.<id>.events`; **control** → `msg.workflow.<id>.control`.

This needs **no core change** (matching the breakdown's "parallel client-side
module"). Promoting the reserved `sx.workflow.*`/`sx_workflows` Layer-0 home to
something clients can use would be a separate core change (a bus op or an allow-list
carve-out) — worth an ADR amendment if we want the reserved addressing to be real.
**Flagged for sirius/lena.**

## Architecture

```
plan.json ─▶ [ coordinator ]  (an ordinary bus client; engine = library)
               │  state Artifact  workflow.<id>  (sextant.workflow/v1, CAS)   ── AC#1/#3
               │  walk steps, checkpoint each transition                       ── AC#2
               │  events ▶ msg.workflow.<id>.events   control ◀ …control       ── AC#2
               └─ step.kind=dispatch ▶ spawn.request ─▶ [M5.2 dispatcher] ─▶ spawn.ack
                                          (await the agent's step-done event)   ── composition
```

- **State is single-writer + CAS** (ADR-0011): the coordinator is the only writer;
  every transition is `UpdateArtifact(expected_rev)`. A conflict re-reads the
  revision and retries (single-writer-by-convention).
- **Idempotent resume**: state is keyed by workflow id, so restarting the
  coordinator with the same `--id` re-reads the envelope and `nextPending()` skips
  steps already `done` — step-granular resume, exactly ADR-0011's promise (not
  Temporal-grade replay). The demo proves it: a re-run dispatches nothing new.
- **Cooperative control** (ADR-0011): `pause`/`resume`/`cancel`/`approve` on the
  control subject ASK — the coordinator honours them at the next safe point (the
  top of the step loop), never mid-step. On (re)start it `settle()`s briefly so a
  control issued while it was down is honoured, not raced past.
- **Composition (no new primitive)**: a `dispatch` step publishes a `spawn.request`
  and correlates the `spawn.ack` by `requestId` — request/reply over pub-sub, the
  M5.2 pattern. **M5.3's request/reply helper was NOT needed** (sirius's call): the
  spawn.ack correlation already covers the one synchronous ask the step-runner has.

## Running it

```
docs/demos/m5-workflow-demo.sh            # build, throwaway bus, all ACs + composition
SX=/path/to/sextant docs/demos/m5-workflow-demo.sh
```

**Token-free**: the dispatched agent is a stub (the `sextant` CLI) that reports its
step done — M5.1 already proved the live `claude -p` / `codex exec` harness, and the
dispatcher is harness-agnostic, so pointing `--harness` at a live `claude -p` wrapper
makes these steps dispatch real agents (see m5-dispatcher-notes.md).

**Live variant** (`docs/demos/m5-workflow-live-demo.sh`, token-bearing): the same
workflow, but each step stands up a REAL `claude -p` agent that joins the bus under
its dispatcher-minted name, does the task, and publishes its step-done
`workflow.event` through the sextant MCP tools — the coordinator records it and
walks on. Built on M5.1's proven recipe (`--bare --strict-mcp-config`, MCP config
pointing `sextant-mcp` at the minted creds; a readiness-retry primer). Run it to
watch real agents driven by a workflow.

## Scope + deferred (PoC)

- One step kind (`dispatch`). A `function`-invoke step (M5.3 `sextant run`) is
  deferred — kept parked (TASK-23) since the spawn.ack pattern covered the need.
- Steps run **sequentially**; the `steps[]` is a flat status list (ADR-0011), no
  dependency graph / fan-out yet.
- Single-writer-by-convention (no fencing token); no engine persistence beyond the
  state envelope; control is `pause/resume/cancel/approve` (approve == resume here).
- Workflow liveness (owner-in-presence + envelope staleness, ADR-0011) and the dash
  workflow cards (TASK-7) render this state but are out of scope here.

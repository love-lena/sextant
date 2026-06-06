---
id: doc-1
title: Roadmap
type: other
created_date: '2026-06-04 18:18'
updated_date: '2026-06-04 22:01'
---

Thin map of the rebuild's milestones — order, goal, definition-of-done, and the
tickets that carry each. **Tickets are the source of truth**; this doc is the
narrative the milestone list can't hold. Live status: `backlog milestone list`.
Numbers are *sequence, not hard gates* (e.g. M3 can be probed early).

## M1 · Core protocol + SDK — ✅ Done
The minimal thing Sextant *is*: the bus (embedded NATS, JWT auth, `sx` namespace),
the wire atom (the frame · epoch · skew validation), the two primitives
(Messages · Artifacts), the Go SDK (connect · operations · drain), and the
clients registry.

## M2 · MVP — clients communicate, manually started
**Goal:** clients talk over the bus, and you can drive + observe them — no dash,
dispatcher, or coordinator.

**Built on ADR-0018 (the keystone re-arch, mid-design):** **Sextant *is* the
bus** — one process that *implements* the operations over a pluggable backend
(behind one internal interface), stamps the **frame**, and enforces identity.
This did **not** change the M2 *endpoint* below — it changed how we reach it (the
SDK is now a client of the bus, not a NATS library). The whole implementation
design is consolidated in **ADR-0019** (call transport · frame stamping ·
bus-minted ULID identity · the backend interface · namespace enforcement ·
SDK-as-bus-client) — the single design review that unlocks the build.

**Done when:** a BYO harness joins via the MCP/skill under its own **bus-minted
identity** and exchanges messages + shares artifacts; a test CLI (`subscribe ·
publish · read · clients · artifact create/update/get/delete/watch` — exact
operation-name parity, no aliases) can drive and observe it; both expose the
**same operation surface** (a conformance test pins the parity mechanically); the
reference clients agree on record shapes; one documented path brings up the bus +
a manual client fleet on a host; and an e2e walkthrough shows ≥2 manually-started
clients exchanging messages + artifacts.

**Tickets (build order):**
- **TASK-29** — implement ADR-0018: the bus implements the operations over the
  backend interface. The foundation; splits into frame · backend interface ·
  bus-serves-operations · SDK-as-client.
- **TASK-30** — client identity: bus-minted ULID primary id + display_name
  (settled before the faces bake in addressing).
- **TASK-28** — test/operator CLI + the **conformance test** (the load-bearing
  "one surface, many faces" guarantee).
- **TASK-22** — MCP server + `claude/channel` + skill: BYO harnesses as
  first-class clients, packaged as a Claude Code plugin.
- **TASK-27** — run ergonomics + getting-started: the documented bring-up + the
  e2e DoD walkthrough.
- TASK-12 — lexicon subset (chat + artifact record shapes) — ✅ Done.

M2 ships **Go only** — the Go SDK, the Go CLI, and the Go MCP server. (The
TypeScript SDK is **Future**, TASK-5.)

## M3 · Cross-machine connectivity — spike, expands later
**Goal:** reach the bus from another host (the real case: over SSH).
**Done when (spike):** the bare-minimum tunnel-only smoke test passes with zero
bind change (`sextant up --port` on host A; `ssh -L` from host B; copied creds;
a client on B reaches the bus through the tunnel), surfacing rough spots —
cross-host clock skew (quarantine), port stability, NATS advertise. *Expands to*
routable bind + TLS + safe creds/conn-info distribution + a quickstart +
multi-host topology when we commit to it.
**Tickets:** TASK-24.

## M4 · The dash (human UI)
**Goal:** a human watches and participates through a forkable TUI client (ADR-0014).
**Design:** ADR-0021 — the dash is a composable pane cockpit (presets + toggle +
reflow + config; widget → surface → dash), settled in the TASK-21 pass.
**Done when:** presence + message-stream + artifact panes compose into a cockpit
default; the dash is a composable pane library (toggle/preset/reflow, btop-style);
detail-on-demand. (Direction set by the `proto/dash-tui` prototype.)
**Tickets:** TASK-7 (umbrella) → 7.1 theme+widget · 7.2 adapter · 7.3 surfaces ·
7.4 layout · 7.5 binary. (TASK-21 design pass — done.)

## M5 · Orchestration (spawn + workflows)
**Goal:** managed coordination on top of plain communication.
**Done when:** the reference Dispatcher honours `spawn-request` (launches a client,
recursion works); the reference Coordinator runs a `sextant.workflow/v1`
end-to-end (state→Artifact, control/events→Messages).
**Tickets:** TASK-25 (Dispatcher / spawn) · TASK-26 (Workflow coordinator).

## Off the line
- **Open design questions** — unresolved decisions gating later work. Several are
  folded into **ADR-0019** for M2 (identity mechanics TASK-8, the backend-contract
  TASK-11, write-precision TASK-9); the rest stay parked (retention TASK-13,
  salvage inventory TASK-14, creds reissue TASK-16, request/reply TBD TASK-23).
- **Future** — deferred-but-wanted (TypeScript SDK, client liveness/heartbeat,
  DAG-CBOR, blob tier, multi-backend, Mastra, golangci-lint).

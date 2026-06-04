---
id: doc-1
title: Roadmap
type: other
created_date: '2026-06-04 18:18'
---

Thin map of the rebuild's milestones — order, goal, definition-of-done, and the
tickets that carry each. **Tickets are the source of truth**; this doc is the
narrative the milestone list can't hold. Live status: `backlog milestone list`.
Numbers are *sequence, not hard gates* (e.g. M3 can be probed early).

## M1 · Core protocol + SDK — ✅ Done
The minimal thing Sextant *is*: the bus (embedded NATS, JWT auth, `sx` namespace),
the wire atom (envelope · epoch · skew), the two primitives (Messages · Artifacts),
the Go SDK (connect · domain verbs · drain), and the clients registry.

## M2 · MVP — clients communicate, manually started
**Goal:** agents talk over the bus, and you can drive + observe them — no dash,
dispatcher, or coordinator.
**Done when:** a BYO harness joins via the MCP/skill under its own identity and
exchanges messages + shares artifacts; a test CLI (`tail · publish · clients ·
artifact get/put/watch`) can drive and observe it; both expose the *same*
domain-verb surface; the reference clients agree on record shapes; one documented
path brings up the bus + a manual client fleet on a host; and an e2e walkthrough
shows ≥2 manually-started clients exchanging messages + artifacts.
**Tickets:** TASK-22 (MCP server + skill) · TASK-28 (test CLI) · TASK-12 (lexicon
subset: chat + artifact shapes) · TASK-27 (run ergonomics + getting-started).

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
**Done when:** presence + dialogue + artifact panes compose into a cockpit default;
the dash is a composable, customizable pane library (swap/arrange panes, btop-style);
detail-on-demand. (Direction set by the `proto/dash-tui` prototype.)
**Tickets:** TASK-7 (dash build) · TASK-21 (dash design pass).

## M5 · Orchestration (spawn + workflows)
**Goal:** managed coordination on top of plain communication.
**Done when:** the reference Dispatcher honours `spawn-request` (launches a client,
recursion works); the reference Coordinator runs a `sextant.workflow/v1`
end-to-end (state→Artifact, control/events→Messages).
**Tickets:** TASK-25 (Dispatcher / spawn) · TASK-26 (Workflow coordinator).

## Off the line
- **Open design questions** — unresolved decisions gating later work (identity
  mechanics, write-precision, retention, salvage inventory, creds reissue,
  request/reply TBD).
- **Future** — deferred-but-wanted (TypeScript SDK, client liveness/heartbeat,
  DAG-CBOR, blob tier, multi-backend, Mastra, golangci-lint).

---
title:          Control-plane milestone — sequencing + tracker
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, control-plane, milestone, epic]
discovered_in:  control-plane RFC (plans/rfc-control-plane.md)
---

Umbrella tracker for the control-plane milestone designed in
`plans/rfc-control-plane.md`: turn `sextantd` from a state *tracker* into a
declarative control plane (the operator declares desired state; one
reconciler is the sole actuator). Shipped as **one milestone**, landed as
the individually-correct, CI-green tickets below.

## Sequencing — and why it's mostly serial

The honest constraint: this is a coherent rewrite of the core, and three
**hot files** are shared by almost every ticket —

- `pkg/sextantproto/agent.go` — the agent record (P0 splits it spec/status;
  P1/P2 add fields);
- `pkg/rpc/handlers/*` — the lifecycle handlers (C0, C2, P0, S0, F0, the
  archive fix all touch them);
- the **reconcile loop** (new in P0; P1/P2/S0/archive each add a branch).

**Rule:** a ticket must not be implemented on a base where a hot file it
touches is about to be rewritten by an unmerged earlier ticket. An agent
building P1 on a pre-P0 schema and handlers is writing against code that's
about to be deleted — guaranteed rework and degraded quality. So we
**serialize the trunk and parallelize only genuinely file-disjoint leaves**,
with a **merge barrier** between waves (each wave fully merges before the
next starts, so every agent begins from a fresh base).

- **Wave 1 — contract foundations (parallel; disjoint trees):**
  [[feat-cp-c0-container-spec-builder]] (handlers / containermgr) **∥**
  [[feat-cp-c1-wire-codegen-ts]] (sextantproto / clients).
- **Wave 2:** [[feat-cp-c2-verbspec-table]] (types.go + rpc.go; needs C1).
  Lands before P0 (shares `rpc.go`).
- **Wave 3 — THE TRUNK, solo:** [[feat-cp-p0-reconcile-spine]]. Rewrites
  `agent.go` + all handlers + introduces the reconciler. **Nothing else in
  flight touching `agent.go`/handlers; nothing downstream starts until P0
  merges.** The single most stale-base-sensitive step.
- **Wave 4 — after P0 merges:** serial through the reconcile-loop file —
  [[feat-cp-p1-recovery]] → [[feat-cp-p2-drift]] →
  [[feat-cp-s0-session-record]] → [[bug-cp-archive-volume-leak]]; with
  [[feat-cp-f0-front-door-authz]] safe to run **in parallel** (it lives in
  `natsboot` + JWT + an admission pre-step — it touches neither the loop nor
  `agent.go`).

**Net order:** `C0 ∥ C1 → C2 → P0 → P1 → P2 → S0 → archive-fix`, with C1
parallel in Wave 1 and F0 parallel in Wave 4. The limited parallelism is the
point — a control-plane rewrite shares too many hot files to fan out safely;
forcing parallelism here trades a little wall-clock for a lot of rework.

**Keystone gate:** C0 **must be merged before P0** — P0's actuator calls the
single-source builder, and auto-restart (P1) on a lossy builder would
automate drift propagation on every recovery.

## Tickets

| # | Ticket | Wave | Depends on |
|---|--------|------|-----------|
| C0 | [[feat-cp-c0-container-spec-builder]] | 1 | — |
| C1 | [[feat-cp-c1-wire-codegen-ts]] | 1 | — |
| C2 | [[feat-cp-c2-verbspec-table]] | 2 | C1 |
| P0 | [[feat-cp-p0-reconcile-spine]] | 3 (solo) | C0, C2 |
| P1 | [[feat-cp-p1-recovery]] | 4 | C0, P0 |
| P2 | [[feat-cp-p2-drift]] | 4 | C0, P0, P1 |
| S0 | [[feat-cp-s0-session-record]] | 4 | C0, P0 |
| F0 | [[feat-cp-f0-front-door-authz]] | 4 (∥) | P0 |
| — | [[bug-cp-archive-volume-leak]] | 4 | P0 |

---
title:          Control-plane milestone — sequencing + acceptance standard + tracker
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
the individually-correct, CI-green tickets below. Ticket shorthand is `ctl`.

## Acceptance standard (every stage — aggressive by design)

Each ticket ships with **all three**:

1. **E2E test.** Exercise the operator-visible behavior end-to-end against a
   **real daemon + real containers** (not just unit/reducer tests). A stage
   isn't done until you can *run* the new behavior and watch it work — this
   is the lesson from the TUI/`agents context` escapes (unit-green ≠ works).
2. **Regression tests, accumulating.** Pin the prior behavior the stage must
   NOT break. The suite **accumulates**: every earlier stage's regression
   tests run in every later stage's CI, so the milestone cannot silently
   regress as it grows.
3. **Expected-breakage declaration.** List explicitly any behavior this stage
   *intentionally* breaks because a later ticket restores/replaces it —
   naming that ticket. Between tickets the system may be transitional, and
   **that's allowed**: declared breakage is not a CI/review failure;
   *undeclared* breakage is.

> Rule of thumb: **undeclared red → stop; declared red (with the restoring
> ticket named) → proceed.**

## Sequencing — and why it's mostly serial

This is a coherent rewrite of the core, and three **hot files** are shared by
nearly every ticket —

- `pkg/sextantproto/agent.go` — the agent record (P0 splits it spec/status;
  P1/P2 add fields);
- `pkg/rpc/handlers/*` — the lifecycle handlers (C0, C2, P0, S0, F0, the
  archive fix all touch them);
- the **reconcile loop** (new in P0; P1/P2/S0/archive each add a branch).

**Rule:** a ticket must not be implemented on a base where a hot file it
touches is about to be rewritten by an unmerged earlier ticket — an agent
building P1 on a pre-P0 schema and handlers is writing against code about to
be deleted (rework + degraded quality). So we **serialize the trunk and
parallelize only file-disjoint leaves**, with a **merge barrier** between
waves.

- **Wave 1 — contract foundations (parallel; disjoint trees):**
  [[feat-ctl-c0-container-spec-builder]] (handlers / containermgr) **∥**
  [[feat-ctl-c1-wire-codegen-ts]] (sextantproto / clients).
- **Wave 2:** [[feat-ctl-c2-verbspec-table]] (types.go + rpc.go; needs C1).
  Lands before P0 (shares `rpc.go`).
- **Wave 3 — THE TRUNK, solo:** [[feat-ctl-p0-reconcile-spine]]. Rewrites
  `agent.go` + all handlers + introduces the reconciler. Nothing else in
  flight touching those files; nothing downstream starts until P0 merges.
- **Wave 4 — after P0 merges:** serial through the reconcile-loop file —
  [[feat-ctl-p1-recovery]] → [[feat-ctl-p2-drift]] →
  [[feat-ctl-s0-session-record]] → [[bug-ctl-archive-volume-leak]]; with
  [[feat-ctl-f0-front-door-authz]] safe to run **in parallel** (it lives in
  `natsboot` + JWT + an admission pre-step — touches neither the loop nor
  `agent.go`).

**Net order:** `C0 ∥ C1 → C2 → P0 → P1 → P2 → S0 → archive-fix`, C1 parallel
in Wave 1, F0 parallel in Wave 4. Limited parallelism is the point — a
control-plane rewrite shares too many hot files to fan out safely.

**Keystone gate:** C0 **must merge before P0** (P0's actuator calls the
single-source builder; auto-restart on a lossy builder automates drift).

## Tickets

| # | Ticket | Wave | Depends on |
|---|--------|------|-----------|
| C0 | [[feat-ctl-c0-container-spec-builder]] | 1 | — |
| C1 | [[feat-ctl-c1-wire-codegen-ts]] | 1 | — |
| C2 | [[feat-ctl-c2-verbspec-table]] | 2 | C1 |
| P0 | [[feat-ctl-p0-reconcile-spine]] | 3 (solo) | C0, C2 |
| P1 | [[feat-ctl-p1-recovery]] | 4 | C0, P0 |
| P2 | [[feat-ctl-p2-drift]] | 4 | C0, P0, P1 |
| S0 | [[feat-ctl-s0-session-record]] | 4 | C0, P0 |
| F0 | [[feat-ctl-f0-front-door-authz]] | 4 (∥) | P0 |
| — | [[bug-ctl-archive-volume-leak]] | 4 | P0 |

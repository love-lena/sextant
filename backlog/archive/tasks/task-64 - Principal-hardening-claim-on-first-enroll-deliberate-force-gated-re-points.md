---
id: TASK-64
title: 'Principal hardening: claim on first enroll, deliberate force-gated re-points'
status: Done
assignee: []
created_date: '2026-06-12 18:40'
updated_date: '2026-06-12 19:54'
labels:
  - feature
  - principal
  - bus
  - security
  - ergonomics
  - 'slug:feat-principal-hardening'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 70000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The principal designation ([[feat-principal-designation]] / TASK-54, ADR-0030) gains an asymmetry the operator asked for (ADR-0031): claiming an UNCLAIMED principal is frictionless; re-pointing an ESTABLISHED one is deliberate.

(1) First-user friction: previously, after `sextant clients register --self`, the principal stayed the bootstrap default and the human had to run `sextant principal set <ulid>` by hand. Now a self-enrolling human seat claims the unclaimed principal as part of first-run.

(2) Re-pointing blast radius: previously `principal.set` overwrote the single most security-critical designation with no ceremony. Now re-pointing an established principal is operator-only and force-gated.

The whole claim/re-point story lives in `principal.set` (an extension over the locked core); the conformance-pinned `clients.register` is untouched. Plan approved by the principal (lena) on msg.topic.principal-hardening, artifact principal-hardening-proposal. Co-sign escalation deferred: [[feat-principal-repoint-cosign]].
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A `register --self` on a bus whose principal is still the bootstrap default claims the new (non-agent) seat as principal with no extra command; e2e verifies `principal get` returns it.
- [x] #2 An auto-minting agent (kind=agent) never claims the principal even as the first self-enroll; the bus rejects an agent target on the claim path.
- [x] #3 Once the principal is established, a later self-enroll does not claim it.
- [x] #4 `register --self --no-principal` opts out of the claim even when eligible.
- [x] #5 `principal set <ulid>` on an established principal is refused without --force and prints current then new; the first claim needs no --force.
- [x] #6 A re-point is observable via the existing principal.watch relay and is bus-logged (old, new, caller).
- [x] #7 Canon updated: ADR-0031 added; CONTEXT.md Principal entry and the sextant skill trust section reflect the claim/re-point asymmetry.
- [x] #8 make lint and make test (race) and go test -tags e2e ./tests/e2e/ all green.
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
As implemented (commit ab21f7a, branch worktree-task-61-principal-hardening):
- principal.set authorization is asymmetric: unclaimed -> bootstrap tier (operator|enroll) may claim, enroll only to a non-agent seat; established -> operator-only + Force. Race-safe via CompareAndSet.
- The enrollment credential gains principal.set publish permission, bus-gated to claim-when-unclaimed only.
- selfenroll.Enroll makes a best-effort, timeout-bounded claim for a self-enrolling human seat; --no-principal opts out; EnrollAgent never claims.
- CLI: `principal set` gains --force and prints current -> new; `register --self` gains --no-principal and reports the claim.
- Loud: re-points flow through the existing principal.watch relay + a bus audit log; no new event subject.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on branch worktree-task-61-principal-hardening (commit ab21f7a); PR pending principal sign-off. ADR-0031. Builds on [[feat-principal-designation]] (TASK-54) and [[feat-principal-trust]] (TASK-53, ADR-0030). Co-sign escalation: [[feat-principal-repoint-cosign]] (TASK-65). Renumbered from a transient TASK-61 (canon #113 took 61/62/63).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in 274dd5a (#114, ADR-0031): a self-enrolling human seat claims an unclaimed principal frictionlessly; re-pointing an established principal is operator-only + --force, observable via principal.watch + bus audit log. Conformance-pinned clients.register untouched.
<!-- SECTION:FINAL_SUMMARY:END -->

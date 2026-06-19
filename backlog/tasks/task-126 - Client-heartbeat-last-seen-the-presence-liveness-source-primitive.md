---
id: TASK-126
title: 'Client heartbeat / last-seen — the presence + liveness source primitive'
status: To Do
assignee: []
labels:
  - feature
  - reliability
  - bus
  - presence
  - keystone
  - slug:feat-heartbeat-presence-primitive
  - P1
  - ready-for-human
dependencies: []
priority: high
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
**Keystone primitive** the crew converged on (2026-06-15). Three workstreams all
hit the same wall: NATS `Connz` is **per-server**, so any hub-Connz-derived view
misses leaf clients in a multi-machine world, and `Connz` can't see a fully-dead
client either. The fix is one shared **client heartbeat / last-seen** signal as
the source of truth for presence + active liveness — replacing per-server Connz
for the multi-machine case.

What converges on it:
- **TASK-125 (leaf presence)** — spike-confirmed: leaf clients appear only in the
  leaf's Connz, never the hub's → `clients_list` marks them OFFLINE though fully
  participating. The sole blocker to shipping leaf-mode.
- **TASK-77 (subscribers)** — a subscribers view derived from hub-Connz misses
  leaf clients the same way; multi-machine wants the heartbeat source.
- **TASK-124 (delivery liveness)** — distinct mechanism (seq-gap = passive
  delivery-stall check) but **composable**: an active heartbeat is the liveness
  FLOOR that catches a fully-dead client when seq-gap can't (covers the
  unknown-cause bar). 124's persist+restore is INDEPENDENT and ships on its own;
  only its mode-D liveness floor consumes this.
- **The long-standing Connz-presence/liveness bug** ([[project_liveness_heartbeat_bug]]).

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A client heartbeat / last-seen primitive: clients emit a periodic liveness signal; the hub records last-seen per client as the presence source of truth (works across leaf links, not just direct hub conns)
- [ ] #2 `clients_list` Online derives from heartbeat/last-seen (with a staleness threshold), NOT solely hub-Connz — so leaf-connected clients show online
- [ ] #3 Composable: TASK-124's liveness can consume it as the active dead-client FLOOR (distinct from, layered with, its passive seq-gap); TASK-77 subscribers can derive from it
- [ ] #4 Heartbeat SHAPE co-designed by vega (leaf-presence) + canopus (124 liveness) before any build; gated through sirius
- [ ] #5 ADR for the presence-source change; flag core-serial (ADR-0022) — it's a bus/client-layer primitive + likely a wire/lexicon addition
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner: vega DRAFTS the unified design (leaf-presence source); canopus co-designs
the SHAPE so it composes with 124's seq-gap (active heartbeat floor vs passive
delivery check — distinct, layered, not conflated). Single-machine Connz stays
fine; this is the multi-machine source of truth. Gate the design through sirius;
likely needs an ADR + core-serial coordination (presence is bus/client-layer,
may add a wire/lexicon heartbeat record). Converges [[feat-leaf-node-topology]]
(125), [[feat-robust-subscription-delivery]] (124), TASK-77 (subscribers),
[[project_liveness_heartbeat_bug]]. Surfaced to Lena as an architecture keystone.
<!-- SECTION:NOTES:END -->

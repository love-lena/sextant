---
id: TASK-125
title: 'Leaf-node topology for multi-machine orchestration (research → design)'
status: To Do
assignee: []
labels:
  - research
  - bus
  - cross-machine
  - slug:feat-leaf-node-topology
  - P1
  - ready-for-agent
dependencies: []
priority: high
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (2026-06-15) wants NATS **leaf nodes** for the multi-machine orchestration
workstream **sooner than later** — pulled forward from TASK-24's deferred AC#7
("Multi-host NATS topology (leaf node / cluster) supported or explicitly
deferred"). Resume the existing cross-machine research (artifact
`m3-cross-machine-research`) focused specifically on leaf nodes.

Why leaf nodes: the TASK-24 runbook ships a tunnel-first path (SSH-forward a
remote client to lena-1's bus). A leaf node is the more robust topology — a
remote machine runs its OWN nats-server as a leaf that links up to the hub,
so remote agents connect to a LOCAL bus that transparently federates subjects
to the hub. Better than a raw tunnel for: reconnect resilience, local-first
latency, multiple remote agents per box, and clean subject namespacing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Research artifact: how sextant's embedded nats-server (ADR-0007) exposes/accepts leaf-node connections; auth model for the leaf link (creds/TLS); what subjects federate
- [ ] #2 Design: a remote box runs a leaf that links to lena-1's hub; remote agents connect to the local leaf + fully participate (messages + artifacts) across the link
- [ ] #3 Compare vs the TASK-24 tunnel approach: when to use which; migration/coexistence
- [ ] #4 Cross-host gotchas carried over: clock skew (ULID quarantine), creds distribution, leaf-link auth/TLS
- [ ] #5 Design gated through sirius before implementation; flag core/SDK vs ops/config and whether it needs an ADR + wire considerations
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Resume `m3-cross-machine-research`. Builds on / complements [[task24-remote-workflow]]
(the tunnel-first runbook). Leaf nodes are a NATS-native topology — likely
ops/config + embedded-server flags rather than a wire change, but confirm.
Owner: vega (Lena's call). Discovered-in: Lena prioritized leaf nodes 2026-06-15.
<!-- SECTION:NOTES:END -->

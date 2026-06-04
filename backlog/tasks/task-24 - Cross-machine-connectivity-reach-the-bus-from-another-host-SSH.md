---
id: TASK-24
title: 'Cross-machine connectivity: reach the bus from another host (SSH)'
status: To Do
assignee: []
created_date: '2026-06-04 17:56'
updated_date: '2026-06-04 18:04'
labels: []
milestone: 'M3: Cross-machine connectivity'
dependencies: []
references:
  - docs/adr/0007-bus-is-nats-no-daemon.md
  - docs/adr/0012-reserved-namespace-and-authn.md
  - docs/adr/0008-clients-are-processes.md
priority: high
ordinal: 23000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Today the bus is reached via a localhost URL in bus.json; clients run on the same host. For Lena's real use, agents run on different machines and connect over SSH. This milestone makes a client on host B reach the bus on host A: the bus binds a routable address, conn-info + per-client creds distribute safely to the remote host, per-client JWT identity holds across the wire, and it works through an SSH tunnel. ACs below are DRAFT — pending Lena's review.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 DRAFT: Bus is reachable from another machine (binds a routable/documented address, not localhost-only)
- [ ] #2 DRAFT: A client on a remote host connects over an SSH tunnel using distributed conn-info + its own creds and fully participates (messages + artifacts)
- [ ] #3 DRAFT: Per-client JWT identity/auth holds across hosts; the epoch gate + ULID skew check tolerate cross-machine clocks
- [ ] #4 DRAFT: The remote connection is authenticated and encrypted (TLS or via the SSH tunnel) — no unauthenticated remote access
- [ ] #5 DRAFT: Creds + conn-info distribution to another machine has a documented, safe path (secrets 0600, nothing leaked)
- [ ] #6 DRAFT: A cross-machine quickstart is documented (bus on host A, harness on host B over SSH)
- [ ] #7 DRAFT: Multi-host NATS topology (leaf node / cluster) is supported or explicitly deferred with rationale
- [ ] #8 DRAFT: Bare-minimum smoke spike FIRST — tunnel-only host-to-host comms with ZERO bind change (sextant up --port on host A; ssh -L from host B; scp the creds; a client on B reaches the bus through the tunnel), run early to surface rough spots (port stability, cross-host clock skew + message quarantine, NATS server advertise) before committing the full milestone
<!-- AC:END -->

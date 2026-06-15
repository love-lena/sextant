---
id: TASK-24
title: 'Cross-machine connectivity: smoke spike (expands later)'
status: To Do
assignee: []
created_date: '2026-06-04 17:56'
updated_date: '2026-06-15 16:58'
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
M3 STARTS AS A SPIKE and expands into the items below when we tackle it. The bare-minimum smoke test needs ZERO code change (the bus binds 127.0.0.1; sextant up --port already exists): on host A run `sextant up --port 4222` and mint+scp a creds file; on host B open `ssh -N -L 4222:127.0.0.1:4222 userA@hostA` and run a client at nats://127.0.0.1:4222 with the copied creds — it reaches A's bus through the tunnel. Then a client on A and one on B exchange a message + share an artifact. Run early to surface rough spots: cross-host CLOCK SKEW (CheckSkew quarantines messages past SkewTolerance — the big one), port stability, and NATS advertise on reconnect. EXPANSION (the ACs below, for when we get to it): routable bind host, TLS, safe creds/conn-info distribution, a cross-machine quickstart, and multi-host topology (leaf node / cluster).
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Dependency note 2026-06-15 (lena): TASK-24 depends on TASK-95 (agentic dev workflow) — the goal is to kick off -p workflows on remote machines, not just connect a manual client. The SSH path is proven; the open question is the -p workflow launch + creds/context handoff.
<!-- SECTION:NOTES:END -->

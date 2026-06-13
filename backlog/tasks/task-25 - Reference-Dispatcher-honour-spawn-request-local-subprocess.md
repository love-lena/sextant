---
id: TASK-25
title: 'Reference Dispatcher: honour spawn-request (local subprocess)'
status: In Progress
assignee: []
created_date: '2026-06-04 18:05'
updated_date: '2026-06-13 02:29'
labels:
  - 'slug:feat-m5-client-standup'
milestone: 'M5: Orchestration (spawn + workflows)'
dependencies: []
references:
  - docs/adr/0009-spawn.md
priority: medium
ordinal: 24000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
M5.2 of the approved M5 breakdown (artifact orchestration-m5-effort, signed off by lena 2026-06-12). Graduate the M5.1 spawn spike (TASK-70, [[feat-m5-spawn-spike]]) into the reference dispatcher: subscribe to spawn.request, launch a new local client process, and spawn.ack returns the new id. Plus a supervisor / agent-runner -- its OWN bus client that implements the SDK (starts as a simple re-invoker, grows into a richer client) -- that subscribes to the spawned agent DM/topics and re-invokes the one-shot harness (claude -p --resume, codex exec resume) on inbound: the wake loop that turns a one-shot into a persistent bus citizen. New bus capability: mint-on-behalf, where the bus provisions scoped creds for a spawned client (sole minter, ADR-0020). NOTE: auto-mint already lets any agent JOIN (ADR-0029); mint-on-behalf instead buys a dispatcher-KNOWN id (for spawn.ack and lifecycle), a resume-STABLE id, a SCOPED per-agent cred (not the bootstrap enroll.creds), and a memorable agent NICKNAME (kind=agent). The dispatcher manages its own children directly (in-bounds per the bright line). mint-on-behalf touches the LOCKED CORE -- a serial change (ADR-0022); the supervisor client is parallel / client-side. Depends on the spike (TASK-70); native dep wired once TASK-70 lands.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 spawn-request message kind defined (with job/parent lineage)
- [x] #2 Dispatcher subscribes to spawn-request and launches a client (local subprocess)
- [x] #3 The spawned client connects under its own identity and participates
- [x] #4 Recursion works: a spawned client can itself publish spawn-requests
- [x] #5 Supervisor / agent-runner is its OWN bus client (implements the SDK; starts as a simple re-invoker, grows into a richer client): subscribes to the spawned agent DM/topics and re-invokes the one-shot harness (claude -p --resume / codex exec resume) on inbound -- the wake loop that makes a one-shot a persistent bus citizen
- [x] #6 mint-on-behalf op provisions SCOPED per-agent creds (bus = sole minter, ADR-0020): the dispatcher knows the spawned id up front (for spawn.ack/lifecycle), the id is resume-stable, and each spawned agent (kind=agent) gets a memorable NICKNAME, not the auto-mint placeholder. SERIAL core change (ADR-0022) -- coordinate with core writers
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Aligned to the approved M5 breakdown (artifact orchestration-m5-effort) by canopus 2026-06-12 on lena sign-off. M5.2. Depends on [[feat-m5-spawn-spike]] (TASK-70, sirius PR #119). Parallel: [[feat-m5-sextant-run]] (M5.3, TASK-23). Composed by [[feat-m5-workflow-coordinator]] (M5.4, TASK-26). Refs ADR-0009/0020/0022/0029.

M5.2 built on branch worktree-feat-m5-dispatcher: cmd/sextant-dispatch (subscribe spawn.request → mint named child → launch harness + supervisor → spawn.ack), spawn lexicon (protocol/lexicons/spawn.{request,ack}.json), recursion + lineage, and AC#6 mint-on-behalf (ADR-0033, locked-core: a kind=dispatcher client mints kind=agent children with its own authority; bus clamps the kind). Self-validating docs/demos/m5-dispatcher-demo.sh = 10/10; pkg/bus/mint_on_behalf_test.go covers the authz. AC#6 core diff coordinated with sirius before merge (ADR-0022 serial). Folds TASK-70 Done+archive.

Revised AC#6 per lena's review (2026-06-12): mint authority INVERTED from a kind=dispatcher allowlist to a spawned-worker fence. The bus stamps spawned_by=<caller> on every mint-on-behalf child (ClientEntry.SpawnedBy); clients.register is authorized from ANY registered client EXCEPT one carrying that marker. So any top-level client can dispatch, but a spawned worker cannot recursively dispatch (no fork-bomb); recursion flows through the dispatcher. Doesn't depend on kind (weakly enforced). New core diff = commit b97ee8f; client-side = fdafe3a; branch force-pushed. Demo still 10/10. Re-review pending (sirius core diff + lena).
<!-- SECTION:NOTES:END -->

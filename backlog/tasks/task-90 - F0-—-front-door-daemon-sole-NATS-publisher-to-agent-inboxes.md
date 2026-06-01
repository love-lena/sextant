---
id: TASK-90
title: 'F0 — front door: daemon = sole NATS publisher to agent inboxes'
status: To Do
assignee: []
created_date: '2026-05-29 14:55'
labels:
  - bug
  - control-plane
  - security
  - 'slug:feat-ctl-f0-front-door-authz'
  - P2
dependencies: []
priority: medium
ordinal: 90000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Make the sole-publisher rule **structural, not conventional**. Today the
operator credential has unrestricted NATS perms (`publish: ">"`), so the
`prompt_agent` gate is a politeness clients *observe*, not a rule the broker
*enforces* — anything could publish straight to `agents.<uuid>.inbox` and
skip validation/audit.

**Fix shape:**
- Split the single account into role-scoped principals: **daemon** (publish
  `>`); **operator CLI** (publish `sextant.rpc.*` only — *not* inboxes;
  subscribe `agents.*.frames`/`.lifecycle`); **sidecar** (scoped to
  `agents.<uuid>.*` via its per-incarnation JWT).
- Add a shared **`decode → default → validate`** admission pre-step in front
  of every handler (mutating-then-validating).
- Add the **`WireEpoch` envelope check**: reject a stale-epoch RPC with an
  actionable `make install` diagnostic.

**Acceptance:**
- **E2E (real NATS + daemon):** a non-daemon credential attempting `publish`
  to `agents.*.inbox` is **rejected by the broker**; a normal prompt via RPC
  still reaches the agent; a stale-`WireEpoch` RPC is rejected with the
  reinstall diagnostic.
- **Regression:** existing operator flows (prompt / chat / list / status)
  still work through the daemon; reads (KV-backed TUIs) still bypass the
  gauntlet.
- **Expected breakage (declared — this is the point):** **direct-inbox
  publishing is now forbidden** — any client or test that published straight
  to `agents.*.inbox` breaks (update those tests to go via RPC); the old
  unrestricted creds must be **regenerated** (operators re-run `make
  bootstrap`/re-auth). Name the cred-rotation step in the PR.

**Depends on:** [[feat-ctl-p0-reconcile-spine]] (handlers in final
write-desired shape for the pre-step). **Sequencing:** Wave 4, **parallel** —
`natsboot` + JWT + admission pre-step; touches neither the reconcile loop nor
`agent.go`. Epoch constant from [[feat-ctl-c1-wire-codegen-ts]]. Part of
[[feat-control-plane-milestone]].
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-ctl-f0-front-door-authz.md
Discovered in: control-plane RFC §10.2 (the front door is a convention, not a guarantee)
Original created_at: 2026-05-29T14:55:00-07:00
<!-- SECTION:NOTES:END -->

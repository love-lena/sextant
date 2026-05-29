---
title:          F0 — front door: daemon = sole NATS publisher to agent inboxes
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [bug, control-plane, security]
discovered_in:  control-plane RFC §10.2 (the front door is a convention, not a guarantee)
---

Make the sole-publisher rule **structural, not conventional**. Today the
operator credential has unrestricted NATS perms (`publish: ">"`), so the
`prompt_agent` gate is a politeness clients *observe*, not a rule the broker
*enforces* — anything could publish straight to `agents.<uuid>.inbox` and
skip validation/audit. The audit isn't authoritative until the side door is
closed.

**Fix shape:**
- Split the single account into role-scoped principals: **daemon** (publish
  `>`); **operator CLI** (publish `sextant.rpc.*` only — *not* inboxes;
  subscribe `agents.*.frames`/`.lifecycle`); **sidecar** (scoped to
  `agents.<uuid>.*` via its per-incarnation JWT, already minted at spawn).
- Add a shared **`decode → default → validate`** admission pre-step in front
  of every handler (mutating-then-validating), so defaulting/validation stop
  being re-inlined per-handler and drifting between `archive` and `stop`.
- Add the **`WireEpoch` envelope check**: reject a stale-epoch RPC with an
  actionable diagnostic (`make install`) — the daemon can't restart the CLI,
  so it fails fast.

**Acceptance:**
- A non-daemon credential **cannot** publish to `agents.*.inbox` (broker
  rejects).
- A stale-epoch RPC is rejected with a reinstall diagnostic.
- Reads still bypass the gauntlet (TUIs reading KV unaffected).

**Depends on:** [[feat-cp-p0-reconcile-spine]] (handlers in their final
write-desired shape for the pre-step). **Sequencing:** Wave 4, **parallel** —
lives in `natsboot` + JWT issuance + the admission pre-step; touches neither
the reconcile loop nor `agent.go`. Epoch constant comes from
[[feat-cp-c1-wire-codegen-ts]]. Part of [[feat-control-plane-milestone]].

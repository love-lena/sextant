---
title:          C0 — single-source buildAgentContainerSpec (lossless restart)
status:         open
priority:       P1
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, control-plane, refactor]
discovered_in:  control-plane RFC §5.4
---

Collapse agent container-spec construction into one
`buildAgentContainerSpec(def)` that is the **sole** builder, so `spawn` and
`restart` cannot drift. Today `spawn` appends 6 mounts and `restart` only 3
— `restart` silently omits `gitconfig`, SSH, and the git-dir mount (the
#50-class bug; three still latent). `buildContainerEnv` already proves the
pattern for env; extend it to the whole spec.

**Why:** `restart` is becoming the universal repair *and* upgrade path (RFC
§3.3). A lossy `restart` under auto-restart (P1) doesn't drop one mount — it
*automates the propagation* of that drift on every recovery. Hard gate for
P0/P1.

**Fix shape:**
- Extract `buildAgentContainerSpec(def)`; make the spec a pure projection of
  the persisted `AgentDefinition`. Legitimate spawn/restart differences (mint
  vs preserve UUID) become explicit params, not the *absence* of code.
- Stamp a **spec-fingerprint label** at build time (sets up P2 drift +
  `wire_epoch`).

**Acceptance:**
- **E2E:** spawn an agent, then `restart` it, against a real daemon + docker;
  `docker inspect` both containers and assert **identical mount sets (all
  six)** and identical env — modulo identity. The three latent mounts
  (`gitconfig`/SSH/git-dir) present on `restart`.
- **Regression:** a spawned agent still boots and reaches `ready`; workspace
  + claude-seed mounts still present; `agents context` still works (the
  bind-mount is untouched at this stage — S0 removes it later).
- **Expected breakage:** none (pure improvement).

**Depends on:** none. **Sequencing:** Wave 1 (∥ [[feat-ctl-c1-wire-codegen-ts]]).
**Must merge before** [[feat-ctl-p0-reconcile-spine]]. Subsumes RFC §10.3
(the three latent mounts). Part of [[feat-control-plane-milestone]].

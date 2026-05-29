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
*automates the propagation* of that drift on every recovery. This is the
**hard gate for P0/P1**.

**Fix shape:**
- Extract `buildAgentContainerSpec(def)`; make the spec a pure projection of
  the persisted `AgentDefinition`. The few legitimate spawn/restart
  differences (mint vs preserve UUID) become explicit params, not the
  *absence* of code.
- Stamp a **spec-fingerprint label** at build time (sets up P2 drift
  detection and the `wire_epoch` label).

**Acceptance:**
- A test asserts `restart`'s spec ≡ `spawn`'s spec modulo identity — all six
  mounts present on both paths.
- The three latent mounts (`gitconfig` / SSH / git-dir) are present on
  `restart`.

**Depends on:** none. **Sequencing:** Wave 1 (∥ [[feat-cp-c1-wire-codegen-ts]]).
**Must merge before** [[feat-cp-p0-reconcile-spine]]. Subsumes RFC §10.3
(the three latent mounts). Part of [[feat-control-plane-milestone]].

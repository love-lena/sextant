---
title:          S0 — session record: drop the bind-mount, frames live + on-demand JSONL backup
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, bug, control-plane]
discovered_in:  control-plane RFC §5.10
---

Remove the persistent `claude-projects` **bind-mount** — the very mount
`restart` had to learn to re-apply in the #50 fix, and a thing both `spawn`
and `restart` must remember. Removing it **kills the #49/#50 mount-drift
class at the root** (the mount can't be forgotten if it doesn't exist).

Split the two jobs the mount was doing:
- **Live view (primary):** `agents context` reads the NATS **frame stream**;
  `--follow` tails frames. No mount.
- **Authoritative backup (on demand):** the `.jsonl` stays the ground-truth
  record (never reconstructed from frames) but is **read on demand** via the
  existing `read_file` / `exec_in_container` facility (`agents context
  --raw` / `--backup`).
- **Post-stop backup:** the reconciler takes a **durable snapshot-on-stop**
  into the agent's data dir when it observes the agent leave `running` —
  needs a copy-from-container capability in `containermgr` for the
  exited-container case (exec needs a *running* container).

Retire `resolveSessionJSONLPath`'s host-dir walk.

**Acceptance:**
- No `claude-projects` mount in the container spec.
- `agents context` (live) works off frames; `--raw`/`--backup` reads the
  `.jsonl` on demand.
- A stopped agent's session is still viewable from the snapshot.

**Depends on:** [[feat-cp-c0-container-spec-builder]] (the mount leaves the
builder), [[feat-cp-p0-reconcile-spine]] (snapshot lives in the loop).
**Sequencing:** Wave 4 — touches the reconcile loop, so serialize after
[[feat-cp-p2-drift]] (or partition the loop file). Part of
[[feat-control-plane-milestone]].

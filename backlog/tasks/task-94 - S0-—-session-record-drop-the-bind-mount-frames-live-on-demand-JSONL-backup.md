---
id: TASK-94
title: 'S0 — session record: drop the bind-mount, frames live + on-demand JSONL backup'
status: To Do
assignee: []
created_date: '2026-05-29 14:55'
labels:
  - feature
  - bug
  - control-plane
  - 'slug:feat-ctl-s0-session-record'
  - P2
dependencies: []
priority: medium
ordinal: 94000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Remove the persistent `claude-projects` **bind-mount** — the mount `restart`
had to learn to re-apply in the #50 fix, and a thing both `spawn` and
`restart` must remember. Removing it **kills the #49/#50 mount-drift class at
the root**.

Split the two jobs the mount did:
- **Live view (primary):** `agents context` reads the NATS **frame stream**;
  `--follow` tails frames. No mount.
- **Authoritative backup (on demand):** the `.jsonl` stays ground-truth
  (never reconstructed from frames) but is **read on demand** via the
  existing `read_file`/`exec_in_container` facility (`agents context
  --raw`/`--backup`).
- **Post-stop backup:** the reconciler takes a **durable snapshot-on-stop**
  into the agent data dir when it observes the agent leave `running` (needs a
  copy-from-container capability in `containermgr` for the exited case).

Retire `resolveSessionJSONLPath`'s host-dir walk.

**Acceptance:**
- **E2E (real daemon + docker):** prompt an agent, view live `agents context`
  off frames; fetch `agents context --raw`/`--backup` and confirm it matches
  the in-container `.jsonl`; **stop the agent and read its snapshot** from the
  data dir.
- **Regression:** session content fidelity preserved (the `.jsonl` is still
  authoritative and byte-complete); `agents context` modes (raw / conversation
  / tools / thinking / usage / tree) still render correctly off the chosen
  source.
- **Expected breakage (declared):** the `claude-projects` bind-mount is
  **removed** — anything reading the host-mounted path directly breaks (none
  in-tree besides `resolveSessionJSONLPath`, which is retired); `agents
  context --follow` switches from file-tail to frame-stream (behavior change
  — document it).

**Depends on:** [[feat-ctl-c0-container-spec-builder]] (mount leaves the
builder), [[feat-ctl-p0-reconcile-spine]] (snapshot in the loop).
**Sequencing:** Wave 4 — touches the reconcile loop, so serialize after
[[feat-ctl-p2-drift]] (or partition the loop file). Part of
[[feat-control-plane-milestone]].
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-ctl-s0-session-record.md
Discovered in: control-plane RFC §5.10
Original created_at: 2026-05-29T14:55:00-07:00
<!-- SECTION:NOTES:END -->

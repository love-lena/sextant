---
id: TASK-152
title: 'Self-healing subs: close two bounded restore residuals in the liveness PR'
status: To Do
assignee: []
labels:
  - bug
  - mcp
  - slug:bug-mcp-self-healing-restore-residuals
  - P3
  - ready-for-agent
dependencies: []
priority: low
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-124 (self-healing subscriptions, ADR-0037) ships persist+restore for the
MCP adapter. Two narrow, bounded, self-healing residuals were deliberately left
open there and documented in ADR-0037's "A known bound" section — both are the
right fit for the **liveness PR** (the seq-gap watchdog + heartbeat, the second
TASK-124 slice), which is already core-touching and can carry the small piece
each needs:

1. **Idle-topic, never-primed, resume-before-first-frame.** A `deliver="new"`
   subscription that has delivered NO frame (cursor still 0) and resumes before
   its first frame is restored live-only, so frames published in the dead window
   are not back-filled (its live relay starts after them). Reading from 0 would
   wrongly replay pre-subscribe backlog the agent skipped by choosing `new`.
   Closing it precisely needs the **stream tail at subscribe time** (catch up
   from the subscribe point), which the bus does not expose MCP-side.

2. **Restore-vs-discard subscribe-window race.** Restore bails (generation check)
   if the client is discarded between subjects, but a discard landing DURING a
   single subject's `subscribe` call can leave one stale entry bound to the
   now-closed client, which the replacement client's restore then skips as
   "already active" — that one subscription stays dead until the next reconnect/
   resume. (An earlier attempt to drop-on-gen-change introduced a worse P1 —
   dropping the *replacement* client's sub on the same map key — so it was
   reverted in favor of documenting the bound.)

Both are **self-healing** (the next reconnect/resume clears the live map and
rebinds) and both are caught by the liveness slice's per-subject sequence-gap
check meanwhile, so they are not silent-loss in practice — the gap is detected
and re-read. This ticket tracks closing them properly in the liveness PR.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria

<!-- AC:BEGIN -->
- [ ] #1: a `deliver="new"` subscription that resumes before its first frame catches up the dead-window frames (via the stream-tail-at-subscribe seq the liveness PR's core getter exposes), without replaying pre-subscribe backlog.
- [ ] #2: a client discard during a restore's `subscribe` call no longer leaves a stale entry that blocks the replacement client's rebind (per-generation sub identity / compare-and-swap on the live map), with NO regression of dropping the replacement sub.
- [ ] Both paths have seam-level regression tests; the full gauntlet stays green.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: TASK-124 persist+restore PR (codex-review rounds). Documented in
docs/adr/0037-subscriptions-and-context-survive-a-session-resume.md ("A known
bound"). Relates to the liveness slice (seq-gap watchdog composing with the
ADR-0036 heartbeat). NOTE: filed from the v0.5 worktree at task-152 to avoid the
cross-worktree numbering collision — renumber on merge if it conflicts.
<!-- SECTION:NOTES:END -->

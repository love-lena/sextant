---
id: TASK-63
title: >-
  True `sextant agents resume <uuid>` — restore a paused agent without losing
  session state
status: To Do
assignee: []
created_date: '2026-05-27 10:45'
labels:
  - feature
  - cli
  - agents
  - lifecycle
  - deferred
  - 'slug:feat-agents-resume-verb'
  - P3
dependencies: []
priority: low
ordinal: 63000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Deferred (2026-05-27)

**Decision: defer until `pause` itself ships.** Nothing in the
daemon today produces `LifecyclePaused` — there is no `pause_agent`
RPC, no kill-with-pause path, no `agents pause` verb. The state is
dead-code in the proto. The current remedy in
`pkg/rpc/handlers/prompt.go`, `cmd/sextant/agents_check.go`, and
`cmd/sextant/chat.go` points at `sextant agents restart` — lossy
(drops session, spawns a fresh incarnation) but functional. That's
acceptable until a real operator workflow surfaces a pause
requirement; revisit then.

## Original open questions (preserved for context)

Three questions before this becomes implementable, all about scope vs. the current architecture:

1. **Does paused even happen in practice?** Nothing in the daemon today *produces* `LifecyclePaused` — there's no `pause_agent` RPC, no kill-with-pause path, no `agents pause` verb. The lifecycle state exists in the proto but is dead-code. Is the intent to add pause as a first-class operator verb, or is paused only ever reachable via a sidecar emitting `transition=paused` from inside (e.g. a long-running tool waiting for input)?
2. **What does resume actually mean?** Options:
   - Re-attach to the existing container + send a "resume" signal the sidecar listens for (preserves session; sidecar wakes the SDK driver).
   - Spawn a fresh container with `--preserve-session` carrying the SessionID forward (re-uses the Claude Code session token; loses container state).
   - Resume is equivalent to restart + the existing `--preserve-session` flag the restart_agent RPC already accepts — at which point this verb is a thin alias.
3. **Is paused worth a verb today, or wait until pause itself ships?** If (1)'s answer is "not yet, defer until pause ships", this ticket is just a placeholder.

## Until this lands

The lifecycle-paused remedies in `pkg/rpc/handlers/prompt.go`, `cmd/sextant/agents_check.go`, and `cmd/sextant/chat.go` point at `sextant agents restart <uuid>`. Lossy but real — operator gets the agent back, just on a fresh incarnation.

## Related

- `pkg/sextantproto/payloads.go` § `LifecyclePausedEvent` — the wire state.
- `pkg/rpc/handlers/restart.go` — `--preserve-session` flag (currently a no-op per [[bug-restart-preserve-session-noop]] — which is itself resolved). A real resume probably routes through this path.
- `cmd/sextant/root_test.go::TestRemedyVerbsResolveInCobraTree` — the regression guard added alongside the remedy fix; new verbs must land there.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-agents-resume-verb.md
Discovered in: Codex adversarial review caught that the paused-agent remedy referenced `sextant agents resume`, a command that doesn't exist; the immediate fix re-pointed the remedy at `agents restart`, but restart is lossy for the paused → running transition (drops session, spawns a fresh incarnation)
Original created_at: 2026-05-27T10:45-07:00
<!-- SECTION:NOTES:END -->

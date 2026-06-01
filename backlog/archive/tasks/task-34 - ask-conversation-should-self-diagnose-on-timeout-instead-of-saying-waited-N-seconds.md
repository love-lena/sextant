---
id: TASK-34
title: >-
  ask / conversation should self-diagnose on timeout instead of saying "waited N
  seconds"
status: Done
assignee: []
created_date: '2026-05-26 15:05'
labels:
  - feature
  - cli
  - operator-experience
  - ergonomics
  - 'slug:feat-ask-conversation-self-diagnose-on-timeout'
  - P2
  - 'closed:fixed'
dependencies: []
priority: medium
ordinal: 34000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Fix

Lives on `streamAskTurn` in `cmd/sextant/chat.go` — the function that
drives the one-shot mode of `sextant agents chat`. The function now
tracks the last lifecycle transition observed during the wait and
routes through `askTimeoutError(timeout, lastLifecycle, agentID)` on
the deadline path. Branches:

- terminal lifecycle (`ended` / `crashed` / `paused` / `archived`) — names the state, prints the remedy command.
- non-terminal lifecycle (`started` / `resumed` / `restarted` / `turn_ended`) — "agent is alive but didn't complete a turn — extend `--timeout` or check `sextant logs --tail 50`".
- no lifecycle envelope at all — "no lifecycle activity — try `sextant logs --tail 50` or extend `--timeout`".

Tests in `cmd/sextant/ask_test.go`'s `TestStreamAskTurnTimeoutEnrichesWithLifecycle` cover every branch (no live daemon required — feeds the lifecycle channel directly).

## Summary

When `sextant ask` (or the chat TUI's send hook) doesn't see a `turn_ended` lifecycle envelope within its timeout window, it returns:

```
sextant: ask: timeout waiting for turn_ended lifecycle (waited 10s)
```

This says *what* happened but nothing about *why*. The operator can't tell whether the agent is slow, paused, ended, or crashed — they have to manually peek the lifecycle stream and inspect Docker.

The CLI already subscribes to the lifecycle stream during the wait. On timeout, before bailing, it should consult the *last known* lifecycle transition and tailor the error message:

```
sextant: ask: agent has lifecycle=ended (since 2026-05-26T00:14:32Z). Restart with `sextant agents restart 2b5fcfe4-…`.
sextant: ask: agent has lifecycle=paused. Resume with `sextant agents resume 2b5fcfe4-…`.
sextant: ask: agent is alive but didn't complete a turn within 10s. Try `sextant logs --tail 50` or extend `--timeout`.
```

## Why P2

This doesn't fix the underlying drift (that's `[[bug-agents-list-stale-lifecycle]]` and `[[bug-prompt-agent-accepts-when-sidecar-gone]]`). But it dramatically shortens the operator's debug loop — they get the right next command in the same line as the error.

## Implementation shape

In `cmd/sextant/ask.go` `streamAskTurn`, the timeout path currently returns `errAskTimeout` directly. Extend it to:

1. Replay the agent's lifecycle subject from a recent point (`--from-seq` or last-5-minutes equivalent) and find the most recent transition.
2. If the transition is `ended` / `crashed` / `archived`, surface that + the suggested remedy.
3. If the transition is `started` / `turn_ended` / `running` and recent, fall back to the current generic timeout message but mention that prompts are accepted but not being processed.

Mirror the same enrichment in the chat TUI's `subscriptionEndedMsg` handler (currently it just calls `tea.Quit` — should surface the lifecycle context first).

## Acceptance

- `TestAskTimeoutSurfacesEndedLifecycle` — replay-based test: lifecycle stream's last envelope is `transition=ended`, `ask` times out, error message contains `"ended"` and `"restart"`.
- `TestAskTimeoutGenericWhenLifecycleIsRunning` — last envelope is `started`/`turn_ended`, error is the generic timeout message.

## Related

- `[[bug-agents-list-stale-lifecycle]]` — the lifecycle field needs to be fresh for this to work usefully.
- `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — root-cause fix; if `prompt_agent` rejected dead agents up front, the timeout path would rarely fire.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-ask-conversation-self-diagnose-on-timeout.md
Discovered in: chat TUI Checkpoint C debugging
Original created_at: 2026-05-26T15:05-07:00
Fixed in: (next commit)
<!-- SECTION:NOTES:END -->

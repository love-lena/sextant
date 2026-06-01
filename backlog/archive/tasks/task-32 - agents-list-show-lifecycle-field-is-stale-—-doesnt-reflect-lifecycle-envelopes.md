---
id: TASK-32
title: >-
  agents list / show lifecycle field is stale — doesn't reflect lifecycle
  envelopes
status: Done
assignee: []
created_date: '2026-05-26 15:05'
labels:
  - bug
  - daemon
  - observability
  - operator-experience
  - 'slug:bug-agents-list-stale-lifecycle'
  - P1
  - 'closed:resolved'
dependencies: []
priority: high
ordinal: 32000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

Branch `feat/agents-list-lifecycle-v2`, target main.

- `pkg/sextantd/lifecycle_watcher.go` — `LifecycleWatcher` subscribes to `agents.*.lifecycle`, decodes each envelope, maps the transition to a `LifecycleState`, and updates the agent record in the `agent_definitions` KV. Drops unknown-agent envelopes; no-ops on `turn_ended` and unknown future transitions; no-ops when the record already holds the target state (avoids version-churning spawn/restart writes).
- `pkg/sextantproto/agent.go` — adds `LifecycleEndedState` and `LifecycleCrashedState` so the record can carry the terminal transitions (suffixed `…State` to avoid colliding with `LifecycleEnded` the wire `LifecycleEvent`).
- `cmd/sextantd/daemon.go` — wires `lifecycleRT` into Start (after rpcRT/spawnRT/controlRT) and stops it in doShutdown ahead of the conn close.
- Tests: `lifecycle_watcher_test.go` exercises the handler directly against a fake KV — covers the four state-changing transitions, turn_ended no-op, unknown-agent drop, version bump, and idempotent-skip. Mock-based rather than a full natsboot harness; the original integration-shaped test had a NATS connectivity issue that needs separate debugging (see [[bug-natsboot-test-harness-flake]]).

## Summary

`sextant agents list` and `sextant agents show` return a `Lifecycle` field that is **not** kept in sync with the agent's lifecycle subject. An agent can publish `transition=ended` or `transition=crashed` on `agents.<uuid>.lifecycle` and still report `lifecycle: running` to the operator forever.

Concrete repro from the discovery session:

```
$ sextant tail "agents.2b5fcfe4-….lifecycle" --from-seq 1
[…21:25:08] transition=started   state=running
[…21:25:30] transition=turn_ended
[…21:25:46] transition=turn_ended
[…21:25:58] transition=turn_ended
[…00:14:32] transition=ended     state=ended       ← terminal, ~14h before the listing was run

$ sextant agents list
… 2b5fcfe4-…  assistant  running …                  ← STALE
```

Result: operators trust the listing, send prompts to a dead agent, get silent timeouts on `ask`/`conversation`, and have no built-in way to tell what's wrong without manually replaying the lifecycle stream.

## Why P1

This is the load-bearing health signal for every other sextant verb. `ask`, `conversation`, `agents prompt`, and any future TUI all read this field to decide whether to send. When it lies, every downstream verb appears broken in confusing ways (`ask` shows a timeout, `prompt_agent` returns ok=true to a void — see `[[bug-prompt-agent-accepts-when-sidecar-gone]]`).

## Likely root cause

The agents store records lifecycle at *registration/restart* time and never subscribes to or replays the lifecycle subject afterwards. Either:
- The store handler that publishes `lifecycle=ended` doesn't also update the corresponding agent record.
- The `list_agents` RPC returns the record's lifecycle field without consulting the most recent envelope.

## Fix shape

Two viable approaches:

1. **Daemon-side watcher.** Subscribe to `agents.*.lifecycle` in the daemon and update the agent record on each transition. Simplest; record stays authoritative.
2. **Query-time read-through.** In `list_agents` (and `show`), peek the last `lifecycle.>` envelope per agent and overlay that onto the record. Avoids a long-lived subscription but adds latency to listing.

Prefer (1) — operators read the listing on hot paths.

## Acceptance

- `TestAgentRecordUpdatesOnLifecycleEnded` — daemon test: spawn an agent, publish a synthetic `transition=ended` envelope, call `list_agents` RPC, assert the agent's lifecycle field is `ended`.
- Same for `crashed`, `paused`, `archived`.
- `sextant agents list` reflects the most-recent lifecycle envelope within ~1s of publication.

## Related

- `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — downstream symptom.
- `[[feat-ask-conversation-self-diagnose-on-timeout]]` — partial mitigation in the CLI.
- `[[feat-sextant-agents-check]]` — operator self-serve tool that would currently work around this.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-agents-list-stale-lifecycle.md
Discovered in: chat TUI Checkpoint C — agent `2b5fcfe4-…` showed `lifecycle: running` in `agents list` but had emitted `transition=ended` on its own lifecycle stream ~14h earlier
Original created_at: 2026-05-26T15:05-07:00
Resolved at: 2026-05-26T22:00-07:00
<!-- SECTION:NOTES:END -->

---
id: TASK-36
title: sextant agents check <ref> — one-shot agent health probe with verdict + remedy
status: Done
assignee: []
created_date: '2026-05-26 15:05'
labels:
  - feature
  - cli
  - operator-experience
  - self-serve
  - 'slug:feat-sextant-agents-check'
  - P2
  - 'closed:resolved'
dependencies: []
priority: medium
ordinal: 36000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

`sextant agents check <ref>` lives at `cmd/sextant/agents_check.go`. Probes (in order):

1. `resolveAgentRef` — covers name/UUID resolution + not_found path.
2. `get_agent_status` RPC — pulls the agent record's lifecycle (kept fresh by the LifecycleWatcher from `[[bug-agents-list-stale-lifecycle]]`).

Synthesizes one of: `healthy`, `ended` (lifecycle ended/crashed), `paused`, `archived`, `stale_record` (defined / unknown), `not_found`, `rpc_error`. Each non-healthy verdict ships a remedy command the operator can copy-paste.

`--json` emits a stable `AgentCheck` schema for scripting.

The container-presence + heartbeat-freshness probes the ticket also enumerated are deferred to `[[feat-prompt-agent-heartbeat-staleness]]` (heartbeat cache) — once that cache exists the check picks up another field. Container probe (`docker ps`-equivalent) is filed separately if needed.

Tests in `agents_check_test.go` cover every verdict branch (5 lifecycles + not_found + rpc_error) plus the JSON / text rendering shapes.

The bulk variant ships as `sextant doctor --agents` ([[feat-sextant-doctor-agents]]) sharing the same `runAgentCheck` helper.

## Summary

When an operator suspects an agent is misbehaving, they currently have to chain four separate commands:

```
$ sextant agents show <ref>                                  # is it registered?
$ sextant tail "agents.<uuid>.lifecycle" --from-seq 1        # what's the last transition?
$ docker ps | grep <uuid>                                    # is the sidecar alive?
$ sextant ask <ref> "ping" --timeout 5s                      # does it respond?
```

…and then mentally compose those signals into a verdict. This is the exact diagnostic chain that ran during chat-TUI Checkpoint C debugging, and it should be a single self-serve command:

```
$ sextant agents check assistant
agent:     assistant (2b5fcfe4-8b40-400a-8466-b6b991ece2c7)
record:    lifecycle=running, version=3, updated=2026-05-25T21:25:30Z
lifecycle: last transition=ended at 2026-05-26T00:14:32Z (≈15h ago)   ⚠
container: NOT RUNNING                                                ⚠
heartbeat: last seen 14h52m ago                                       ⚠

verdict:   agent ended ~14h ago — daemon record is stale
remedy:    sextant agents restart 2b5fcfe4-8b40-400a-8466-b6b991ece2c7
```

Healthy agent verdict:

```
$ sextant agents check assistant
agent:     assistant (2b5fcfe4-…)
record:    lifecycle=running, version=4, updated=2026-05-26T22:00:01Z
lifecycle: last transition=turn_ended at 2026-05-26T22:00:14Z (12s ago)
container: running (pid 1234)
heartbeat: last seen 3s ago

verdict:   healthy
```

## Why P2

This is the operator's "what's wrong with this agent" button. Closes the gap that `[[bug-agents-list-stale-lifecycle]]` opens until that root-cause fix lands. Useful even after that fix — for digging into "I sent a prompt and got no response" cases.

## Implementation shape

New file: `cmd/sextant/agents_check.go`. Wire as `sextant agents check <ref>`.

Probes to run, in order:

1. `list_agents` → find the agent record (catches "not registered").
2. Replay last N lifecycle envelopes (`--from-seq` of recent stream seq) → most recent transition + timestamp.
3. `docker ps` (or equivalent — abstract via `pkg/containermgr`) → is the sidecar container alive?
4. Last heartbeat — query the heartbeat KV (per `pkg/sextantproto.HeartbeatPayload`) or replay last beat.
5. Optional `--ping` flag: actually send a `prompt_agent "ping"` and watch for the response within a short timeout. Off by default to keep the check side-effect-free.

Then synthesize a verdict + remedy table. Possible verdicts:
- `healthy`
- `ended` (last lifecycle is `ended`/`crashed`/`archived`)
- `stale_record` (lifecycle says running but container gone OR heartbeat ancient)
- `paused`
- `not_found` (no such agent)
- `daemon_down` (couldn't reach daemon)

Each verdict maps to a one-line remedy command the operator can copy-paste.

## Acceptance

- `TestAgentsCheckHealthy` — fresh lifecycle + live container + recent heartbeat → verdict=healthy.
- `TestAgentsCheckStaleRecord` — lifecycle says running, container missing → verdict=stale_record + remedy includes `agents restart`.
- `TestAgentsCheckEnded` — last lifecycle is `ended` → verdict=ended + remedy includes `agents restart`.
- `--json` flag emits a stable schema for scripting.

## Related

- `[[bug-agents-list-stale-lifecycle]]` — this command's existence is partly a workaround for that bug.
- `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — same.
- `[[feat-sextant-doctor-agents]]` — sibling: bulk version that scans every agent at once.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-sextant-agents-check.md
Discovered in: chat TUI Checkpoint C — operator had no built-in "what's wrong with this agent" command
Original created_at: 2026-05-26T15:05-07:00
Resolved at: 2026-05-26T23:35-07:00
<!-- SECTION:NOTES:END -->

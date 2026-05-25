---
title: restart_agent does not forward ANTHROPIC_API_KEY to the restarted container
status: resolved
priority: P1
created_at: 2026-05-24T23:18-07:00
resolved_at: 2026-05-25T01:00-07:00
labels: [bug, sdk-wireup, restart, auth]
discovered_in: post-wire-up smoke verification
resolution: Factored the env-var construction into pkg/rpc/handlers/container_env.go's buildContainerEnv helper and call it from both spawn.go and restart.go; restart now forwards ANTHROPIC_API_KEY (plus SEXTANT_MODEL and SEXTANT_PERMISSION_MODE, which were also dropped). TestRestartForwardsAPIKey pins the regression. Shipped in the same commit as [[bug-restart-preserve-session-noop]].
---

## Summary

The `restart_agent` RPC (`sextant agents restart`) creates a new container for an existing agent definition but does not inject `ANTHROPIC_API_KEY` into the container's environment. The freshly-restarted sidecar then fails with "Not logged in" on the next prompt because the Claude Agent SDK has no credentials.

## Repro

1. `sextantd &` with `ANTHROPIC_API_KEY` in its environment
2. `sextant agents spawn smoke --template default` — works; agent responds to prompts
3. `sextant agents kill smoke` — container removed; agent goes to `defined`
4. `sextant agents restart smoke --preserve-session`
5. `docker exec <new-container> env | grep ANTHROPIC` — returns nothing
6. `sextant agents prompt smoke "say ack"` — SDK fails: `Not logged in`

## Impact

After any operator-triggered restart, agents stop responding to prompts. The spawn-path wire-up (commit `f796467`) didn't extend to the restart path.

## Proposed fix

Factor the env-var construction out of `pkg/rpc/handlers/spawn.go:294-330` (the block that builds `envVars`) into a shared helper:

```go
buildContainerEnv(def, jwt, sessionId, hostID, natsURL, natsUser, natsPassword, mcpURL) map[string]string
```

Both `spawn.go` and `restart.go` call it. Restart additionally passes `def.Runtime.SessionID` when `--preserve-session` is set (related: [[bug-restart-preserve-session-noop]]).

## Acceptance

`TestRestartForwardsAPIKey`: spawn an agent, kill it, restart with `--preserve-session`, assert `docker exec <new-container> env | grep ANTHROPIC_API_KEY` returns the daemon's value.

## Related

- Wire-up commit: `f796467 spawn: forward SEXTANT_MODEL, SEXTANT_SESSION_ID, ANTHROPIC_API_KEY`
- [[bug-restart-preserve-session-noop]]

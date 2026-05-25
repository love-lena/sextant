---
title: restart_agent --preserve-session flag is a no-op
status: open
priority: P1
created_at: 2026-05-24T23:18-07:00
labels: [bug, sdk-wireup, restart, session-continuity]
discovered_in: post-wire-up code reading
---

## Summary

The `--preserve-session` flag on `sextant agents restart` is recorded but does not actually resume the Claude session. Per the comment in `pkg/rpc/handlers/restart.go`:

> "PreserveSession is recorded but has no effect today — M12 ships no session-continuity machinery (no driver loop). The flag is accepted for forward-compat so a future restart-with-session handler doesn't have to break the wire shape."

The wire-up commit (`d95b570`/`f796467`) shipped session-continuity machinery (SDK driver loop, session_id persisted via CAS in `agent_definitions` KV). The assumption in that comment is now stale. The flag should pass `def.Runtime.SessionID` into `SEXTANT_SESSION_ID` for the restarted container.

## Impact

Operators can't restart an agent and continue its conversation. Every restart starts a fresh SDK session, dropping prior context.

## Proposed fix

In `restart.go`, after resolving the AgentDefinition, when `args.PreserveSession` is true, read `def.Runtime.SessionID` and include it in the new container's `SEXTANT_SESSION_ID` env var (via the shared `buildContainerEnv` helper from [[bug-restart-no-api-key-forwarding]]).

Update the restart.go comment to remove the obsolete claim.

## Acceptance

`TestRestartPreservesSession`:
1. Spawn agent, prompt with `"remember the number 42"`
2. Kill agent
3. Restart with `--preserve-session`
4. Prompt `"what number did I tell you to remember?"`
5. Assert response contains `"42"`

## Related

- [[bug-restart-no-api-key-forwarding]] (same code path, same fix scope — bundle both fixes)
- Wire-up commit `d95b570 sidecar: drive Claude Agent SDK on inbox prompts`

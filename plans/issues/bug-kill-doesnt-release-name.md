---
title: agents kill leaves agent in 'defined' state — name never released
status: resolved
priority: P2
created_at: 2026-05-24T23:18-07:00
resolved_at: 2026-05-25T00:00-07:00
labels: [bug, lifecycle, agents-cli]
discovered_in: phase-1 smoke verification
resolved_by: feat-lead-f2db6f38-001 (bundle with [[feat-agents-archive-cli-verb]])
---

## Resolution

Bundle fix landed on branch `feat-lead-f2db6f38-001`. Both options from
the proposed fix shipped together so an operator can pick the
ergonomics that suit the moment:

- **Option A — `kill --archive` flag**: `sextant agents kill <agent>
  --archive` now issues `kill_agent` + `archive_agent` against the
  same UUID. The name is released as soon as the kill returns.
- **Option B — explicit `archive` verb**: `sextant agents archive
  <agent>` (and the bulk `--all-dead`) flip lifecycle to archived
  directly. See [[feat-agents-archive-cli-verb]] for the verb's
  surface and MCP tool registration.

The new `archive_agent` RPC lives at `pkg/rpc/handlers/archive.go`.
`agentNameInUse` already excluded archived definitions, so flipping
lifecycle is the only step required to release the name.

Pinned by `TestArchiveAgentReleasesName` and `TestKillWithArchiveFlag`
in `pkg/rpc/handlers/archive_test.go`. Both assert the exact
spawn → kill → archive → spawn-again-succeeds shape this issue
specifies.

## Summary

`sextant agents kill <agent>` terminates the container and transitions lifecycle to `defined`, but the agent's name remains in `agent_definitions` KV. Per `architecture.md` §2 only the `archived` state releases names. Since the `archive` verb isn't exposed in the M12 CLI (see [[feat-agents-archive-cli-verb]]), every test run permanently claims a name.

## Repro

1. `sextant agents spawn echo-test --template default` — succeeds
2. `sextant agents kill echo-test` — succeeds; container removed
3. `sextant agents spawn echo-test --template default` — fails: `agent name "echo-test" is already in use`

## Impact

- Smoke tests must use unique names every run (e.g. `smoke-$(date +%s)`)
- The KV bucket accumulates stale `defined` entries
- After many test cycles, name pool pollution becomes noticeable

## Proposed fix (pick one or both)

**Option A** — when killing an agent with no preservable state (no session_id, or operator passes `--archive`), auto-archive instead of leaving in `defined`. Likely gated by a `--archive` flag on `kill`.

**Option B** — fix [[feat-agents-archive-cli-verb]] so operators can manually archive after kill. Less convenient but cleaner separation of concerns.

Lean: ship both. `--archive` flag for the common case, separate `archive` verb for explicit lifecycle moves.

## Acceptance

`TestKillThenSpawnSameName`: spawn `agent-foo`, kill `agent-foo --archive`, spawn `agent-foo` again succeeds without error.

## Related

- [[feat-agents-archive-cli-verb]]
- `architecture.md` §2 (Identity rules)

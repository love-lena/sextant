---
id: TASK-6
title: Add `sextant agents archive` CLI verb (M12 gap)
status: Done
assignee: []
created_date: '2026-05-24 23:18'
labels:
  - feature
  - cli
  - lifecycle
  - 'slug:feat-agents-archive-cli-verb'
  - P2
  - 'closed:resolved'
dependencies: []
priority: medium
ordinal: 6000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

Bundle fix landed on branch `feat-lead-f2db6f38-001`. The catalog gap
is closed end-to-end:

- **RPC handler**: `pkg/rpc/handlers/archive.go` (`NewArchiveAgent`).
  Registered as verb `archive_agent` with capability `control.archive`
  in `pkg/rpc/types.go`. Stops a live container first (mirroring
  `kill_agent`) so an operator archiving a running agent doesn't have
  to pair the call. Idempotent on already-archived agents.
- **CLI verb**: `sextant agents archive <agent>` accepts either UUID
  or name (resolved via `list_agents`). `--all-dead` archives every
  agent currently in lifecycle=defined in one shot.
- **MCP tool**: `archive_agent` added to the tool catalog
  (`pkg/mcpserver/tools.go`, `pkg/mcpserver/server.go`). The agent's
  tool list now advertises it; `CapForTool(ToolArchiveAgent)` returns
  `control.archive`.
- **`kill --archive` flag**: convenience pairing for the common case
  ([[bug-kill-doesnt-release-name]] Option A).
- **JSON schemas**: regenerated via `go run ./cmd/sextantproto-gen`;
  `archive_agent_request.json` + `archive_agent_response.json` now
  ship under `pkg/sextantproto/schemas/` for the TypeScript client.

Pinned by `TestArchiveAgentReleasesName`,
`TestArchiveAgentOnRunningAgentStopsContainer`,
`TestArchiveAgentIdempotent`, `TestArchiveAgentUnknownReturnsNotFound`,
`TestKillWithArchiveFlag`, and `TestArchiveAllDead` in
`pkg/rpc/handlers/archive_test.go`.

## Summary

`archive_agent` is in the RPC catalog (`specs/protocols/rpc-catalog.md` line 37 — `control.archive` capability) and is the lifecycle transition that releases an agent's name (`architecture.md` §2). The M12 CLI does not expose it — `sextant agents <verb>` supports only list/show/spawn/kill/restart/prompt.

## Impact

Operators cannot archive an agent through the CLI. Names are permanently claimed because [[bug-kill-doesnt-release-name]] leaves agents in `defined`. The MCP tool catalog also doesn't include `archive_agent` per the smoke run (15 tools listed in `sextant-sidecar` startup log).

## Proposed fix

Add `cmd/sextant/agents.go` verb that issues the `archive_agent` RPC. CLI surface:

```
sextant agents archive <agent>     # archive a living-or-defined agent
sextant agents archive --all-dead  # archive every agent currently in `defined` (bulk cleanup)
```

The MCP tool catalog also needs `archive_agent` added to `pkg/mcpserver/` so agents can archive their own children when appropriate.

## Acceptance

1. `sextant agents archive --help` prints the verb's help.
2. `sextant agents archive <uuid>` transitions the agent to `archived`; subsequent `sextant agents list` does not include it by default (use `--all` to include archived).
3. The name is reusable: `sextant agents spawn <same-name> --template default` succeeds.

## Related

- [[bug-kill-doesnt-release-name]]
- `specs/protocols/rpc-catalog.md` (catalog entry exists)
- `specs/architecture.md` §2 (lifecycle states)
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-agents-archive-cli-verb.md
Discovered in: phase-1 smoke verification
Original created_at: 2026-05-24T23:18-07:00
Resolved at: 2026-05-25T00:00-07:00 (by feat-lead-f2db6f38-001 (bundle with [[bug-kill-doesnt-release-name]]))
<!-- SECTION:NOTES:END -->

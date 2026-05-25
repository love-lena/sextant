---
title: Add `sextant agents archive` CLI verb (M12 gap)
status: open
priority: P2
created_at: 2026-05-24T23:18-07:00
labels: [feature, cli, lifecycle]
discovered_in: phase-1 smoke verification
---

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

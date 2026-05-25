---
title: Add `sextant templates reload` CLI verb (avoid daemon restart for template edits)
status: open
priority: P3
created_at: 2026-05-24T23:18-07:00
labels: [feature, cli, templates]
discovered_in: operator workflow (lead.toml creation)
---

## Summary

Today, the only way to sync a newly-added template file into NATS KV is to restart sextantd. The sync happens once at daemon startup in `buildSpawnRuntime` (`cmd/sextantd/spawn.go:75`). Re-running `sextant init` writes the file but doesn't push to KV since the running daemon doesn't re-scan the dir.

Operators editing or adding templates have to stop+start sextantd every time — which also drops every live agent (and trips [[bug-shutdown-orphan-clickhouse]]).

## Proposed fix

Add CLI verb:

```
sextant templates reload [--config-dir <path>]
```

That publishes a control envelope on `sextant.control.templates_reload` (new subject). Sextantd subscribes to this subject; on receipt, calls `templates.SyncDirToKV(ctx, tplKV, cfg.Paths.TemplatesDir)` and replies with the count of templates synced.

Subject + RPC pattern matches the existing M7 wire semantics. Could also be exposed as an MCP tool for agents that need to register their own templates.

## Acceptance

`TestTemplatesReload`:
1. Sextantd up with 1 template (default)
2. Write a new file `~/.config/sextant/templates/lead.toml`
3. `sextant templates reload` — exits 0; output reports `synced 2 template(s)`
4. `sextant agents spawn x --template lead` — succeeds without daemon restart

## Related

- `cmd/sextantd/spawn.go:75` (current sync location)
- `specs/architecture.md` §11b (Templates section already documents reload as deferred)
- [[bug-shutdown-orphan-clickhouse]] (current workaround forces daemon restart, which trips that bug)

---
title: Template-declared seeding source for /home/agent/.claude
status: open
priority: P2
created_at: 2026-05-25T01:25-07:00
labels: [feature, template, sidecar, agent-memory]
discovered_in: assistant-agent daily-drive scoping
---

## Summary

Today every spawned agent's `/home/agent/.claude/` is an empty per-agent named volume. Per `architecture.md` §3 "Open sub-decisions", `.claude` volume seeding was left open with the lean "template can declare a `.claude` seeding source." That seeding mechanism is the missing piece for agents with persistent personal memory — e.g. a daily-drive assistant agent that needs a `CLAUDE.md` of operator preferences, scratch memory files, custom slash commands, hooks, and `settings.json`.

## Impact

**Blocks**: any agent class that needs pre-loaded persistent memory or Claude-Code-CLI customization. Most acute for assistant-style agents (one persistent agent the operator daily-drives) but also relevant for specialist dev agents (e.g. a "frontend-dev" agent class with `.claude` populated with frontend-specific slash commands).

**Workaround today**: `docker exec <container> cp -r ...` after every spawn — works, but is operator-toil and breaks on re-spawn. The clean answer is template-declared seeding.

## Proposed fix

Add a new template field `claude_seed` (host path, optional). When set, the spawn handler bind-mounts the host path read-only into the container at `/home/agent/.claude/` instead of using the empty per-agent named volume. The agent can read its memory; writes from inside the agent are container-local (don't bleed back to the host source).

If two-way sync is needed later, an `claude_seed_mode = "copy-on-spawn"` option could be added that initializes the per-agent volume from the host path on first spawn (the agent then owns its own copy, with operator-side merge tools).

Schema:

```toml
# In ~/.config/sextant/templates/assistant.toml
name = "assistant"
# ... existing fields ...
claude_seed = "~/.config/sextant/assistant-claude"   # optional; absent = empty volume (current behavior)
# claude_seed_mode = "readonly-bind" (default) | "copy-on-spawn" (future)
```

The host path expansion follows the same `os.UserHomeDir()` pattern as other config paths. Missing source dir is a template-validation error (template invalid; spawn rejected with a clear message).

Implementation:
1. `pkg/templates/template.go` — add `ClaudeSeed string \`toml:"claude_seed"\``
2. `pkg/templates/template.go::Validate` — if set, fail-fast if the path doesn't exist or isn't a directory
3. `pkg/rpc/handlers/spawn.go` — when `tpl.ClaudeSeed != ""`, add a bind-mount of `tpl.ClaudeSeed` → `/home/agent/.claude` to the container spec. Default (unset) keeps the current per-agent-volume behavior.
4. `specs/architecture.md` §3 — close the "claude volume seeding" open question; §11b — document the new template field.

## Acceptance

`TestSpawnedContainerSeedsClaudeFromHostPath`:

1. Make a host dir `/tmp/test-claude-seed/` with a stub `CLAUDE.md` containing the line `marker: spawn-seed-acceptance`.
2. Write a template with `claude_seed = "/tmp/test-claude-seed"`.
3. Spawn an agent with that template.
4. `docker exec <container> cat /home/agent/.claude/CLAUDE.md | grep 'marker: spawn-seed-acceptance'` exits 0.

Plus `TestTemplateValidationRejectsMissingClaudeSeed`: template with `claude_seed = "/nonexistent"` fails validation at load time with a clear error.

## Related

- `architecture.md` §3 "Open sub-decisions" — this resolves the `.claude` volume seeding open question
- `architecture.md` §11b — template schema
- Future: a "save my memory back to the host source" operator workflow (out of scope here; just the read path)
- Discovered when scoping daily-drive assistant agents — see also `[[bug-restart-no-api-key-forwarding]]` + `[[bug-restart-preserve-session-noop]]` (assistant needs cross-restart conversation continuity)

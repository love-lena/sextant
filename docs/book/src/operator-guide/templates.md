# Templates

Templates describe what an agent looks like when you spawn it. They live as TOML files in `~/.config/sextant/templates/<name>.toml` and are synced into the NATS KV `templates` bucket.

For the loader-side details, see [templates](../components/templates.md). This chapter is operator-facing: writing templates and the choices you'll make.

## The default template

`sextant init` writes `~/.config/sextant/templates/default.toml`:

```toml
name = "default"
description = "Minimal spawnable agent — assistant-style, broad reads, restricted writes."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "read.history", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
```

This is what `sextant agents create <name> --template default` consumes.

## Full schema

```toml
name             = "researcher"
description      = "Reads code and writes summaries; no merge rights."
image            = "sextant-sidecar:latest"

permissions = [
  "read.agents",
  "read.history",
  "control.prompt",
  "control.worktree",        # only if the agent needs worktree manipulation
]

env = {
  SEXTANT_CUSTOM_FLAG = "yes",
}

mounts = ["worktree"]         # named mount classes: "worktree" | "ssh" | "secrets"

initial_prompt   = "You are a research assistant…"  # systemPrompt for every turn
model            = "claude-opus-4-7[1m]"
permission_ceiling = "auto"                          # "" | "auto" | "plan"

# Optional: pre-seed the agent's ~/.claude (slash commands, hooks, CLAUDE.md)
claude_seed      = "~/.config/sextant/researcher-claude"
claude_seed_mode = "copy-on-spawn"                   # "copy-on-spawn" | "readonly-bind"
```

Field validation lives at `pkg/templates/template.go:97-133`.

### `name`
The unique template identifier and the file stem.

### `image`
The Docker image to run. Almost always `sextant-sidecar:latest` (or a pinned `:<sha>`). A custom-per-agent-image story is in the architecture spec but not implemented at this snapshot — the operator's options are the standard image or a manually built derivative.

### `permissions`
The capability allowlist. The spawn handler signs the agent's JWT with exactly these strings. Capabilities map to MCP tools (see [mcpserver](../components/mcpserver.md)) and to RPC verbs (see [RPC catalog](../protocols/rpc-catalog.md)).

> **Descoping rule** (architecture §9c): a spawned agent's permissions are a *subset* of the spawner's. An agent that lacks `control.spawn` can't grant itself or its children `control.spawn`. Sextantd validates the subset at spawn time.

### `env`
Extra env vars to inject into the container. Don't put secrets here — use `mounts = ["secrets"]` instead.

### `mounts`
Named mount classes the daemon resolves into bind mounts and volumes at spawn. The allowlist is enforced by `pkg/templates/template.go:KnownMountClasses()` — unknown class names fail template validation.

| Class       | What it mounts                                                  | Mode |
|-------------|-----------------------------------------------------------------|------|
| `worktree`  | The agent's git worktree → `/workspace`                         | rw   |
| `ssh`       | Operator's `~/.ssh` → `/home/agent/.ssh`                        | ro   |
| `secrets`   | Per-template subset of `~/.config/sextant/secrets/` → `/run/sextant/secrets/` | ro |

If `mounts` includes `worktree`, the spawn handler creates a worktree named `feat-<template>-<short_uuid>-001` and mounts it. If it doesn't, the agent has no `/workspace` — useful for read-only agents.

**`ssh`** (added by `feat-container-ssh-passthrough`): opt-in passthrough of the operator's SSH config and keys, mounted read-only. The whole `~/.ssh` directory comes through; there is no per-template filtering. Default templates do **not** include `ssh` — only declare it on templates that need to push to a private remote or `ssh` to a host. The spawn handler resolves it at `pkg/rpc/handlers/spawn.go:413-423`.

**`secrets`** is reserved — see [Known gaps and drift](../reference/known-gaps.md). The class is in `KnownMountClasses()` so template validation passes, but the spawn handler does not yet wire a mount for it.

### `initial_prompt`
Persistent context. The spawn handler base64-encodes this and injects it as `SEXTANT_INITIAL_PROMPT`; the sidecar decodes it and passes it to the SDK as `systemPrompt` on every turn. **Not** a first user message — use a regular `prompt_agent` call for greetings. See `plans/issues/bug-initial-prompt-not-forwarded-to-sdk.md`.

### `model`
The Claude model identifier. Default `claude-opus-4-7[1m]` (sidecar default at `images/sidecar/entrypoint/src/index.ts:62`). The model also accepts `claude-sonnet-4-6`, `claude-haiku-4-5-20251001`, etc.

### `permission_ceiling`
The maximum SDK `permissionMode`:

- `""` (unset) → no ceiling above whatever the sidecar resolves.
- `"auto"` → SDK runs in auto-edit mode. Default.
- `"plan"` → SDK runs in plan-only mode (read-only).

Per `specs/architecture.md` §10b, `auto` is the hard cap — `bypassPermissions` is never granted.

### `claude_seed`
A host directory containing pre-built CLAUDE.md, slash commands, hooks, or `settings.json`. The path supports `~/` expansion. If set, this directory becomes the agent's `/home/agent/.claude`.

### `claude_seed_mode`
How the seed is delivered:

| Mode               | Behaviour                                                                                | When to use                                          |
|--------------------|------------------------------------------------------------------------------------------|------------------------------------------------------|
| (unset or `"copy-on-spawn"`) | On first spawn, copy host dir into a per-agent named volume; mount rw. Volume survives restarts. | Default. Assistant-style multi-turn agents. |
| `"readonly-bind"`  | Bind-mount the host dir read-only.                                                       | One-shot agents that don't need session resume.      |

> **Caveat**: `"readonly-bind"` breaks SDK session resume because the SDK can't write its journal under `projects/<encoded-cwd>/<session-id>.jsonl`. Pick `"copy-on-spawn"` (or omit) unless you're sure. See `plans/issues/bug-claude-seed-readonly-breaks-session-persistence.md`.

## Reloading after edits

Edit a `.toml` file, then either:

```bash
sextant templates reload
```

or use the MCP `templates_reload` tool from inside an agent with `control.templates`. Both call `templates.SyncDirToKV` and push the new shape into the KV bucket. Newly spawned agents pick it up immediately; live agents keep their existing definition until restarted.

## Writing a new template

1. Copy `default.toml` to `<name>.toml` in the templates dir.
2. Edit fields. Keep `image = "sextant-sidecar:latest"` unless you've built a derivative.
3. Adjust `permissions` to the smallest set that lets the agent do its job.
4. If the agent needs custom slash commands or hooks, prepare a host dir and point `claude_seed` at it.
5. `sextant templates reload`.
6. `sextant agents create <name> --template <name>`.

## Spawned worktree naming

When a template's `mounts` includes `worktree`, the spawn handler generates a deterministic worktree name with `pkg/worktree.SpawnWorktreeName`:

```
feat-<template>-<short_uuid>-001
```

`<short_uuid>` is the first 8 chars of the agent's UUID. The `feat-` prefix is fixed so the generated name passes `ValidateName`. The agent can later create its own task-shaped worktree via `worktree_create` and move its work there.

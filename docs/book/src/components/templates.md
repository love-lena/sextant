# templates

**Source**: `pkg/templates/`.

`templates` loads and validates agent templates from TOML files on disk and syncs them into a NATS KV bucket so RPC handlers can read them cheaply.

## When to reach for this component

- You're writing or auditing the template schema.
- You want to know what `claude_seed_mode` actually does at spawn time.
- You're investigating "template not found" or template validation failures.

## Public surface

| Symbol                                       | File:line                       | Purpose                                                    |
|----------------------------------------------|---------------------------------|------------------------------------------------------------|
| `Template`                                   | `pkg/templates/template.go:24`  | TOML-tagged struct.                                        |
| `(t Template) Validate()`                    | `:97`                           | Field-level checks.                                        |
| `(t Template) ResolveClaudeSeedMode()`       | `:83`                           | Effective seed mode (`""` / `"copy-on-spawn"` / `"readonly-bind"`). |
| `ExpandClaudeSeed(path)`                     | `:143`                          | Resolve `~/` in `claude_seed` path; assert directory.      |
| `LoadFromFile(path)`                         | `:163`                          | Read one `.toml`.                                          |
| `LoadDir(dir)`                               | `:192`                          | Read every `*.toml` lexically; fail on any failure.        |
| `SyncToKV(ctx, kv, tpls)`                    | `:233`                          | Marshal + Put each.                                        |
| `SyncDirToKV(ctx, kv, dir)`                  | `:258`                          | LoadDir + SyncToKV.                                        |
| `LoadFromKV(ctx, kv, name)`                  | `:274`                          | Get + Unmarshal + Validate.                                |
| `ErrNotFound`                                | `:297`                          | Sentinel for "template not in KV".                         |
| `KV` interface                               | `:222`                          | Minimal NATS KV surface (`Put`, `Get`, `ListKeys`).        |
| `Bucket` const                               | `:19`                           | `"templates"` — the NATS KV bucket name.                   |

## Schema

```toml
name             = "default"                 # also the file stem
description      = "..."
image            = "sextant-sidecar:latest"
permissions      = ["read.agents", "read.history", "control.prompt"]
env              = { KEY = "value" }
mounts           = ["worktree"]              # named mount classes
initial_prompt   = ""                         # base64'd later; passed to SDK as systemPrompt
model            = "claude-opus-4-7[1m]"
permission_ceiling = "auto"                  # "" | "auto" | "plan"
claude_seed      = "~/.config/sextant/assistant-claude"  # optional host dir
claude_seed_mode = "copy-on-spawn"            # optional; default when claude_seed set
```

Required-field validation (`pkg/templates/template.go:97-133`):

- `name`, `image`, `permissions` are required; permissions must be non-empty.
- `permission_ceiling`: empty, `"auto"`, or `"plan"`.
- `claude_seed`: if set, must expand to an existing directory.
- `claude_seed_mode`: empty, `"copy-on-spawn"`, or `"readonly-bind"`.

`ResolveClaudeSeedMode()`:

| `claude_seed` | `claude_seed_mode`  | Effective mode      |
|---------------|---------------------|---------------------|
| unset         | unset               | `""` (no seed)      |
| set           | unset               | `"copy-on-spawn"`   |
| set           | `"copy-on-spawn"`   | `"copy-on-spawn"`   |
| set           | `"readonly-bind"`   | `"readonly-bind"`   |

## Seed mode behaviour at spawn

The spawn handler reads the resolved seed mode and acts:

- **`""`** — `/home/agent/.claude` is an empty per-agent named volume (default).
- **`"copy-on-spawn"`** — `EnsureVolume("sextant-claude-seed-<uuid>")`; on first spawn, populate from `claude_seed` host dir; mount rw at `/home/agent/.claude`. The Claude Agent SDK's session journal under `projects/<encoded-cwd>/<session-id>.jsonl` survives across restarts. Use this for assistant-style agents that need multi-turn resume.
- **`"readonly-bind"`** — bind-mount the host seed dir read-only at `/home/agent/.claude`. **Resume does not work** because the SDK can't write its journal. Suitable for one-shot agents that genuinely don't need persistence.

## On-disk layout

Templates live in `Paths.TemplatesDir` (default `~/.config/sextant/templates/`). Each `.toml` is one template; the file stem becomes the default `name` if the TOML omits it.

`sextant init` seeds `default.toml`:

```toml
name = "default"
description = "Minimal spawnable agent — assistant-style, broad reads, restricted writes."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "read.history", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
```

(Source: `specs/architecture.md` §11b.)

## NATS KV sync

At `sextantd` startup (and on `sextant templates reload` / the `templates_reload` MCP tool), `SyncDirToKV` walks the templates directory and pushes every parsed template into the `templates` KV bucket keyed by template name. RPC handlers read from KV, not from disk, so a stale file on disk doesn't break the spawn path — until reload is called.

`LoadFromKV` re-validates on read, so a corrupted KV entry surfaces as an error rather than as a downstream Docker failure.

## Test coverage

`pkg/templates/template_test.go` covers `LoadFromFile`, `LoadDir`, `Validate`, `ExpandClaudeSeed`, and `LoadFromKV` (including `ErrNotFound`).

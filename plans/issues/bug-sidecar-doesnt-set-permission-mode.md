---
title: Sidecar SDK driver doesn't set permissionMode — agents can't write/edit/bash without a granter
status: resolved
priority: P1
created_at: 2026-05-25T00:24-07:00
resolved_at: 2026-05-25T00:34-07:00
resolution: "spawn handler maps template permission_ceiling to SEXTANT_PERMISSION_MODE env var; sidecar reads it and passes permissionMode to sdkOpts"
labels: [bug, sidecar, sdk-wireup, permissions]
discovered_in: first sextant-driven dispatch attempt
---

## Summary

The sidecar's SDK driver invokes the Claude Agent SDK without setting `permissionMode`. The SDK defaults to its interactive `"default"` mode, which prompts the operator on every Edit, Write, Bash, etc. Inside a non-interactive container there is no one to grant, so the tool call returns an error: `Claude wants to make a write change to /workspace/Makefile, but you haven't granted it yet.`

Agents can read freely (Read, Glob, Grep don't require grants) but can't take any action. Effectively, agents are stuck the moment they try to mutate anything.

The template field `permission_ceiling = "auto"` exists in the schema and is loaded by `pkg/templates/`, but the sidecar never reads it — the field is currently decorative.

## Repro

1. Spawn an agent with any template that has worktree + control caps (e.g. `lead`).
2. Prompt the agent to make any file edit ("create a file at /workspace/hello.txt").
3. `sextant conversation <uuid>` shows the Edit tool_call followed immediately by a `tool_result is_error=true` with the grant message.

Reproduced 2026-05-25 00:22 PT with dev-1 / 900e58d6-… on the first sextant-driven dispatch attempt.

## Impact

**Blocks every agent dev task.** Same severity as [[bug-worktree-gitdir-unreachable-in-container]] — the agent has the workspace, has the git, has the API key, but can't actually change anything.

## Proposed fix

In `images/sidecar/entrypoint/src/index.ts::newSDKDriver`, read the agent's effective permission mode from the env / template and pass it to the SDK:

```typescript
const sdkOpts: Record<string, unknown> = {
  model: env.model,
  permissionMode: env.permissionMode,  // see mapping below
};
```

Mapping from template `permission_ceiling` to Claude Agent SDK `permissionMode`:

| template.permission_ceiling | sdk.permissionMode |
|---|---|
| `"auto"` (default) | `"acceptEdits"` — auto-accept Edit/Write; auto-classifier gates Bash and other dangerous ops |
| `"plan"` | `"plan"` |
| `"default"` | `"default"` (no auto-accept; for interactive operator use only — never set by sextantd in non-interactive flows) |

Critically: **never `"bypassPermissions"`**. Per `[[sextant-permission-ceiling]]` memory, `auto` is the max ceiling for any pod; bypassPermissions is never allowed even on operator request.

Plumbing:
1. `pkg/rpc/handlers/spawn.go` — read `tpl.PermissionCeiling` and pass as `SEXTANT_PERMISSION_MODE` env var (mapped to SDK value before sending — keep the template's enum sextant-internal, don't leak SDK enum names into TOML).
2. `images/sidecar/entrypoint/src/index.ts` — parse `SEXTANT_PERMISSION_MODE`, default to `"acceptEdits"` if unset (matches the spec's "auto is default" semantic).

## Acceptance

`TestAgentCanEditWorkspaceFile`:

1. Sextantd with worktree wired
2. Spawn an agent with `lead` template (permission_ceiling = "auto")
3. Prompt: `"write the word ok to /workspace/test.txt"`
4. Within 30s, `docker exec <container> cat /workspace/test.txt` returns `ok`
5. The SDK's tool_call for Write/Edit returned success (not the grant-required error)

Plus: `TestPermissionCeilingPlanBlocksMutations` — spawn with `permission_ceiling = "plan"`, prompt for an edit, assert tool call denied with plan-mode error.

## Related

- [[sextant-permission-ceiling]] — never bypass; auto is max
- `specs/architecture.md` §11b — template `permission_ceiling` field
- Wire-up commit `d95b570 sidecar: drive Claude Agent SDK on inbox prompts` introduced the SDK call without setting permissionMode
- This is the third prerequisite (after [[bug-worktree-gitdir-unreachable-in-container]] + [[feat-container-git-config]]) needed before sextant-driven dev tasks can actually complete.

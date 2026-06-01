---
id: TASK-15
title: >-
  SDK acceptEdits mode auto-allows Edit/Write only — Bash still requires
  per-call approval
status: Done
assignee: []
created_date: '2026-05-25 00:50'
labels:
  - bug
  - sidecar
  - sdk-wireup
  - permissions
  - 'slug:bug-sidecar-bash-still-asks-in-acceptedits'
  - P1
  - 'closed:resolved'
dependencies: []
priority: high
ordinal: 15000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

`@anthropic-ai/claude-agent-sdk`'s `permissionMode: "acceptEdits"` auto-allows the Edit / Write / MultiEdit tools but **does not** auto-allow Bash. Every Bash call from inside the agent — `git add`, `git commit`, `make build`, even `git status` followed by another bash op — returns `This command requires approval` with no interactive granter available, leaving the agent permanently blocked at the first bash step.

This is the SDK behaving as documented; sextant's intent was to match Claude Code CLI's `--permission-mode auto`, which is broader (file edits + safe-bash classifier-driven approvals). The mapping in [[bug-sidecar-doesnt-set-permission-mode]] correctly hit the strictest non-bypass SDK mode, but that strict mode isn't enough for an agent that needs to commit + run tests.

## Repro

1. Sextantd running with worktree wired, sidecar image rebuilt with permissionMode patch (commit `c209640`).
2. Spawn agent with `lead` template (permission_ceiling = "auto").
3. Prompt it to edit a file then `git add` + `git commit`.
4. The Edit succeeds (`is_error=false`). The first `git add` returns `tool_result is_error=true result="This command requires approval"`. Every subsequent Bash also returns the same.

Reproduced 2026-05-25 00:48 PT with `dev-2` agent — full transcript in `/tmp/dev-2-convo.log`.

## Impact

**Blocks every agent dev task that involves commits, tests, builds, or any shell command.** Same severity as the original permission-mode bug. The fix in commit `c209640` got us one layer further (Edit works), but the next layer (Bash) is still gated.

The agent CAN reason about and edit files. It CAN'T:

- Stage + commit changes (`git add` / `git commit`)
- Run tests (`make test` / `go test`)
- Build (`make build`)
- Run worktree merge cleanup (the merge MCP tool works, but pre-merge verification doesn't)
- Use Glob/Grep outputs as inputs to follow-up Bash (the chain breaks at the first bash)

## Proposed fix

Two viable directions:

**Option A — `canUseTool` callback that auto-allows Bash with a safe classifier.** This is what Claude Code CLI does internally under `--permission-mode auto`. The callback inspects each tool invocation and returns `'allow'` for safe operations and `'ask'` / `'deny'` for dangerous ones.

Concretely in `images/sidecar/entrypoint/src/index.ts::newSDKDriver`, after building `sdkOpts`:

```typescript
sdkOpts.canUseTool = (toolName, input) => {
  // Edit / Write / MultiEdit / Read / Glob / Grep / TodoWrite — always allow
  // (acceptEdits would also allow these; redundant but explicit).
  if (SAFE_TOOLS.has(toolName)) return { behavior: 'allow', updatedInput: input };

  if (toolName === 'Bash') {
    const cmd = String(input.command ?? '');
    if (isDangerous(cmd)) {
      return { behavior: 'deny', message: `dangerous bash refused: ${cmd}` };
    }
    return { behavior: 'allow', updatedInput: input };
  }

  // MCP tools are gated server-side by JWT; SDK can always allow.
  if (toolName.startsWith('mcp__')) return { behavior: 'allow', updatedInput: input };

  // Unknown — be safe.
  return { behavior: 'deny', message: `unknown tool: ${toolName}` };
};
```

`isDangerous(cmd)` denies a small bright-line set: `rm -rf /` and `rm -rf ~`, `curl … | sh`, `dd …`, `:(){:|:&};:`, `sudo …` (containers don't need sudo for anything in their workspace). Everything else is allowed.

**Option B — `allowedTools: string[]` whitelist.** Simpler but blunter: explicitly list every tool the agent is allowed to use, including Bash, without a dangerous-pattern filter. The SDK auto-allows anything in the list.

```typescript
sdkOpts.allowedTools = ['Bash', 'Edit', 'Write', 'MultiEdit', 'Read', 'Glob', 'Grep', 'TodoWrite'];
```

Lean **Option A**. It's slightly more code but mirrors the CLI's `auto` semantics and preserves a brittle line of defense against agent self-pwn (the classifier denies the obvious footguns even if a model decides to type `rm -rf ~`).

## Acceptance

Build on the existing `TestAgentCanEditWorkspaceFile` (commit c209640):

`TestAgentCanCommitInWorkspace`: spawn lead agent, prompt:

```
edit /workspace/test.txt to contain "ok", then `git add test.txt`, then `git commit -m test`. reply with the resulting commit SHA.
```

Within 60s, assert:

1. The agent's reply includes a 40-char hex SHA.
2. `docker exec <container> git -C /workspace log -1 --format=%H` matches that SHA.
3. No `tool_result is_error=true` lines from approval rejections in the conversation log.

Plus: `TestBashClassifierDeniesDangerousCommands`: prompt the agent to `rm -rf /workspace` — assert the dangerous-command guard fires (tool_result with the deny message). Note this test runs against the mock driver since exercising the real classifier doesn't require an API call.

## Related

- [[bug-sidecar-doesnt-set-permission-mode]] (just resolved at c209640 — opened the Edit door; this one opens the Bash door)
- `[[sextant-permission-ceiling]]` memory — `bypassPermissions` still forbidden; this fix uses `canUseTool` to broaden without bypass
- Wire-up commit `d95b570` introduced the SDK call; this is the third layer of permission-mode work on the same call site
- [[feat-container-ssh-passthrough]] — once this lands, agents can run `git push`; ssh mount becomes the next blocker for push specifically (commits + merges work without it)
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-sidecar-bash-still-asks-in-acceptedits.md
Discovered in: first end-to-end sextant-driven dispatch attempt (dev-2)
Original created_at: 2026-05-25T00:50-07:00
Resolved at: 2026-05-25T01:05-07:00
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
added canUseTool callback in newSDKDriver using safe-bash classifier (Option A); classifier extracted to src/classifier.ts with 64 unit tests
<!-- SECTION:FINAL_SUMMARY:END -->

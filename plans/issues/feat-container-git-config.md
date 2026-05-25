---
title: Containers don't have git user.name/user.email — agent commits will fail
status: open
priority: P2
created_at: 2026-05-24T23:18-07:00
labels: [feature, container, sidecar, git]
discovered_in: pre-flight gap analysis
---

## Summary

The sidecar container has `git` installed but no `user.name` / `user.email` configured. When an agent runs `git commit`, the command will refuse (`Please tell me who you are`) or use unhelpful Docker default values.

## Impact

Agents cannot land commits inside their worktree. The whole "agent writes code → commits → merges" loop fails on the first commit.

## Proposed fix

In `pkg/rpc/handlers/spawn.go`, write a `/home/agent/.gitconfig` into the container at spawn time:

```gitconfig
[user]
    name = sextant <agent-name>
    email = <agent-uuid>@sextant.local
[init]
    defaultBranch = main
```

Either via a `docker cp` after `docker create` and before `docker start`, or via a per-spawn temp file bind-mounted into the container.

Future: per-template override (`gitconfig` field) to let operators pin specific identities for trusted agent classes that should commit as a real person.

## Acceptance

`TestSpawnedContainerHasGitConfig`: spawn an agent, exec `git config --global --get user.name` inside, assert returns `sextant <name>`. Then exec a real `git commit` against a temp repo and assert it succeeds.

## Related

- `specs/components/sidecar-image.md` volume mounts table
- M14 worktree work (`worktree_merge` depends on agent commits being valid)
- [[feat-container-ssh-passthrough]] (related; both needed for full commit + push flow)

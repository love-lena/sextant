---
title: Worktree's .git pointer file references a host path that doesn't exist inside the container — agent git ops fail
status: open
priority: P1
created_at: 2026-05-24T23:58-07:00
labels: [bug, worktree, container, sidecar, m14]
discovered_in: first sextant-driven dispatch attempt
---

## Summary

When sextantd creates a per-agent worktree via the M14 worktree manager and bind-mounts it into the container as `/workspace`, the worktree contains a `.git` file (not directory) with a `gitdir:` line pointing at the host's absolute path:

```
gitdir: /Users/lena/dev/sextant-initial/.git/worktrees/feat-default-<uuid>-001
```

That path **does not exist inside the container's filesystem**. Every git operation inside `/workspace` fails with `fatal: not a git repository: <that-path>`.

## Repro

1. Spawn an agent with `mounts = ["worktree"]` (e.g. the `default` or `lead` template) against a sextantd with `worktree.repo_root` configured.
2. `docker exec <container> sh -c 'cd /workspace && git status'`
3. Output: `fatal: not a git repository: /Users/lena/dev/sextant-initial/.git/worktrees/feat-default-<uuid>-001`

Verified during the first sextant-driven dispatch attempt on 2026-05-24 with daemon `sextantd: worktree manager ready (repo=/Users/lena/dev/sextant-initial worktrees_root=/Users/lena/dev/sextant-worktrees)`.

## Impact

**Blocks every agent dev task.** No agent can read its branch, commit, diff, or push. The agent can read the working-tree files (they're bind-mounted from the host worktree), but cannot interact with git in any way.

This is the difference between "agent has files" and "agent can do git work" — and the latter is what every sextant-driven dev task needs.

The implementor's `TestM14WorktreeAcceptance` test passed because it exercised worktree_create from the test harness (host context), where the absolute paths in `.git` are valid. The container scenario was not validated.

## Proposed fix

Three viable approaches, in increasing order of architectural correctness:

**Option A — bind-mount the main repo's .git dir at the same path inside the container.** Read-only. The `.git` pointer file resolves identically inside and outside the container because the path matches. Adds one bind to every spawned container. Exposes the entire main repo's git history to the agent (read-only), which is mild — they have a worktree of it anyway.

**Option B — rewrite the worktree's `.git` file to use a container-visible path AND bind-mount the gitdir to that path.** E.g. rewrite to `gitdir: /sextant/gitdir`, bind-mount `<host-path>/.git/worktrees/<branch>` → `/sextant/gitdir`. The host's view becomes inconsistent (host git operations on that worktree path break unless they also use the rewritten file). Annoying for operator debugging.

**Option C — use bind mounts to make the gitdir path identical inside and outside the container.** Mount only the agent's specific worktree gitdir into the container at the same path. Doesn't expose main `.git` to the agent. But the gitdir has an `objects` symlink pointing at `../../../objects` (main repo's objects), and that breaks unless main `.git/objects` is also mounted. Drives you back toward Option A.

Lean: **Option A** for simplicity. Mark the main `.git` mount as read-only at the kernel level so the agent literally cannot mutate it; only its own `.git/worktrees/<branch>` subdir is writable.

Implementation lives in `pkg/containermgr/` (extra mount when materializeWorkspace returns a real worktree) + `pkg/rpc/handlers/spawn.go` (pass the gitdir path through).

## Acceptance

`TestSpawnedContainerCanGitCommit`:

1. Sextantd with `worktree.repo_root` set
2. Spawn an agent with `mounts = ["worktree"]`
3. `docker exec <container> sh -c 'cd /workspace && git status'` exits 0, shows the worktree's branch
4. `docker exec <container> sh -c 'cd /workspace && git config user.email a@b && git config user.name x && echo hi > new && git add new && git commit -m test'` succeeds
5. `docker exec <container> sh -c 'cd /workspace && git log -1 --format=%s'` reports `test`
6. The commit is visible from the host: `cd <worktree-host-path> && git log -1 --format=%s` reports `test` too

## Related

- M14 worktree work (`pkg/worktree/`, materializeWorkspace in `pkg/rpc/handlers/spawn.go`)
- `TestM14WorktreeAcceptance` — passes for host-side worktree usage; doesn't cover container case
- [[feat-container-git-config]] — also needed for agent commits (user.email/name)
- This issue + [[feat-container-git-config]] are the minimum prerequisite set before ANY sextant-driven dev task can complete

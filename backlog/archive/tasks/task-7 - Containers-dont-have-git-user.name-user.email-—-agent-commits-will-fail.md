---
id: TASK-7
title: Containers don't have git user.name/user.email — agent commits will fail
status: Done
assignee: []
created_date: '2026-05-24 23:18'
labels:
  - feature
  - container
  - sidecar
  - git
  - 'slug:feat-container-git-config'
  - P2
  - 'closed:resolved'
dependencies: []
priority: medium
ordinal: 7000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
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
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-container-git-config.md
Discovered in: pre-flight gap analysis
Original created_at: 2026-05-24T23:18-07:00
Resolved at: 2026-05-25T00:00-07:00
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Per-spawn temp file under WorkspaceRoot (`gitconfig-<agent-uuid>`), bind-mounted read-only into the container at `/home/agent/.gitconfig`. Identity is `sextant <agent-name>` / `<agent-uuid>@sextant.local`. Rollback ledger removes the file on any spawn failure. Acceptance covered by TestSpawnedContainerHasGitConfig (real docker exec) + the unit tests TestSpawnAgentWritesGitConfigMount and TestSpawnAgentRollsBackGitConfigOnContainerFailure.
<!-- SECTION:FINAL_SUMMARY:END -->

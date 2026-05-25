# worktree

**Source**: `pkg/worktree/`.

`worktree` manages git worktrees on behalf of agents. Each running agent typically has its own worktree mounted into its container as `/workspace`. The package also owns the merge lock that serializes all merges into `main`.

## When to reach for this component

- You want to know what `worktree_create` / `worktree_merge` actually run.
- You're investigating a stuck merge lock.
- You need to add a new merge strategy or change branch-naming validation.

## Public surface

| Symbol                                                                 | File:line                       | Purpose                                                  |
|------------------------------------------------------------------------|---------------------------------|----------------------------------------------------------|
| `Manager`                                                              | `pkg/worktree/worktree.go:99`   | The manager. Construct via `New`.                        |
| `New(cfg Config)`                                                      | `:105`                          | Validate config; ensure `WorktreesRoot` exists.          |
| `ValidateName(name)`                                                   | `:132`                          | Check `<kind>-<desc>-<seq>` regex.                       |
| `(m *Manager) Create(ctx, name, base, owner)`                          | `:145`                          | Create on disk + registry.                               |
| `(m *Manager) Destroy(ctx, name, force)`                               | `:197`                          | Remove dir + entry.                                      |
| `(m *Manager) List(ctx)`                                               | `:227`                          | All registered worktrees.                                |
| `(m *Manager) Get(ctx, name)`                                          | `:270`                          | One worktree.                                            |
| `(m *Manager) Diff(ctx, name, against)`                                | `:289`                          | `git diff <against>...<branch>`.                         |
| `(m *Manager) Merge(ctx, name, target)`                                | `:318`                          | Locked merge via transient worktree.                     |
| `SpawnWorktreeName(template, agentUUID)`                               | `:564`                          | Helper to name agent-spawn worktrees.                    |
| `AcquireMergeLock(...)`                                                | `pkg/worktree/lock.go:64`       | Public lock primitive (used by `Merge`).                 |
| `LockValue`                                                            | `pkg/worktree/lock.go:34`       | The JSON shape stored in `locks.merge`.                  |

## Configuration

```go
type Config struct {
    RepoRoot       string       // operator's main checkout
    WorktreesRoot  string       // parent dir for per-task worktrees
    Registry       RegistryKV   // worktrees bucket
    Locks          LockKV       // locks bucket (required for Merge)
    HolderID       string       // identity for the lock value
    MergeLockTTL   time.Duration // default 5 min
    Now            func() time.Time // injected for tests
}
```

`sextantd` wires this from `WorktreeConfig.RepoRoot` and `WorktreeConfig.WorktreesRoot`. When `RepoRoot` is empty, the daemon skips wiring the worktree surface entirely — `worktree_*` RPCs and MCP tools return "not configured" errors.

## Branch / name validation

Names must match `<kind>-<short-desc>-<seq>` (`pkg/worktree/worktree.go:132`) where `kind ∈ {feat, fix, refactor, docs, test, chore, spec}`. Examples: `feat-bus-routing-001`, `fix-clickhouse-migration-003`, `spec-nats-component-001`.

`SpawnWorktreeName(templateName, agentUUID)` (`pkg/worktree/worktree.go:564`) generates `feat-<template_name>-<short_uuid>-001`, where `<short_uuid>` is the first 8 chars of the agent's UUID. The `feat-` prefix is fixed so the generated name passes `ValidateName` *as long as* the template name contains only `[a-z0-9]` characters and dashes — a template named `assistant_writer` would yield a name that fails the branch-name regex (`pkg/worktree/worktree.go:52`). Keep template names lowercase kebab-case to avoid this.

## Create flow

`Create(ctx, name, baseBranch, owningAgent)`:

1. `ValidateName(name)`.
2. Default `baseBranch` to `"main"` if empty.
3. Probe registry for duplicate. Return `ErrAlreadyExists` if present.
4. Check the on-disk path doesn't exist.
5. Run `git worktree add -b <name> <path> <baseBranch>`.
6. Build a `WorktreeInfo{Name, Path, Branch, BaseBranch, OwningAgent, Status: "active", CreatedAt, LastActivity}`.
7. Write to the registry. On registry-write failure, roll back with `git worktree remove`.

## Destroy flow

`Destroy(ctx, name, force)`:

1. Get the entry from the registry. Return `ErrWorktreeNotFound` if absent.
2. Unless `force == true`, refuse non-archived worktrees (`ErrStatusGuard`).
3. `git worktree remove [--force] <path>`.
4. Delete the registry entry.

## Merge flow — the serialized happy path

`Merge(ctx, name, target)` is the single merge primitive. Defaults `target` to `"main"`.

1. Pre-lock probe of the source worktree (cheap "does this name exist" check).
2. Acquire `locks.merge` with wait (see below).
3. **Re-Get under the lock.** Closes a TOCTOU window where a peer merged the same worktree between probe and lock-acquire. If status is already `merged`, return success idempotently.
4. Mark status `merging`.
5. Clean up any stale `.merge-*` worktrees left from prior crashes.
6. Allocate a transient worktree at `<WorktreesRoot>/.merge-<rand>/`.
7. `git worktree add --detach <merge_dir> <target>`. Detached so the operator's main checkout (which may be on the target branch) doesn't conflict.
8. `git -C <merge_dir> merge --no-ff <branch>` with `sextantd` as the committer identity.
9. **On conflict**: `git merge --abort`, tear down the transient worktree, mark status `conflict`, return `MergeResult{OK: false, Conflicts: [...]}`. The source branch is untouched; the operator can resolve and retry.
10. **On clean merge**: `git rev-parse HEAD` in the transient, then `git update-ref refs/heads/<target> <new-sha>`. The target ref advances atomically in the shared `.git`. Mark status `merged`. Tear down the transient (`git worktree remove`).
11. Release the lock.

The operator's main checkout is never touched throughout. The transient worktree always tears down — its cleanup uses a background context so a caller cancel can't strand it.

**Out of scope for this snapshot (M14):**

- No remote push. Merges are local-only.
- No concurrent merges across different targets. The single lock serializes everything.
- No CI gate. The merge proceeds unconditionally on a clean result.

## Merge lock

Bucket `locks`, key `merge`, value JSON:

```json
{
  "holder": "sextantd@<host>",
  "acquired_at": "2026-05-25T15:30:00.000000Z",
  "ttl_seconds": 300
}
```

`AcquireMergeLock(ctx, kv, holder, ttl, now)` (`pkg/worktree/lock.go:64`):

1. Try `kv.Create(merge, value)`. Success → lock is ours; return a release closure.
2. On exists: read the existing value, check expiry via `now()`. If still valid, return `ErrLockHeld`.
3. If expired (stale): delete and retry `Create` once. Lose the race → `ErrLockHeld`.

`Merge` wraps `Acquire` in a poll loop that retries every 50ms up to one TTL deadline.

The release closure uses a 5-second background context so caller cancellation doesn't strand the lock. A crashed holder is recoverable: the next attempt sees the lock value, finds `acquired_at + ttl_seconds < now`, deletes it, and proceeds.

## Test coverage

`pkg/worktree/worktree_test.go` covers Create, Destroy, List, Get, Diff, Merge (clean), Merge (conflict), and the TOCTOU re-Get under lock. `pkg/worktree/lock_test.go` covers acquire/release, expiry, and the stale-cleanup path.

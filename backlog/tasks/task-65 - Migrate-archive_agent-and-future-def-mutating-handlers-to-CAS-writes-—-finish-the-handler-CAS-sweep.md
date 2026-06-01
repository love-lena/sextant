---
id: TASK-65
title: >-
  Migrate archive_agent (and future def-mutating handlers) to CAS writes —
  finish the handler-CAS sweep
status: To Do
assignee: []
created_date: '2026-05-27 11:25'
labels:
  - feature
  - daemon
  - rpc
  - lifecycle
  - follow-up
  - 'slug:feat-handlers-cas-writes'
  - P2
dependencies: []
priority: medium
ordinal: 65000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## What's already landed in this PR

The CAS pattern is fully in place — interface, real-KV adapter, fake KV in tests, and two handlers migrated:

- **Interface**: `pkg/rpc/handlers/spawn.go` — `AgentMutableKV` gained `Update(ctx, key, value, revision) (uint64, error)` (commit `ab0eebe`).
- **Real-KV adapter**: `cmd/sextantd/spawn.go` — `kvMutableAdapter.Update` wires through to `jetstream.KeyValue.Update`.
- **Test fake**: `pkg/rpc/handlers/spawn_test.go` — `fakeMutableKV` now tracks per-key revisions; `Update` enforces CAS semantics (returns `jetstream.ErrKeyExists` on mismatch); existing tests untouched (they use the Put path).
- **`restart_agent` migrated** (`ab0eebe` + `0632eaa`): captures `initialDefRevision` after the first Get; the final commit loop bails on **any** revision drift (catches archive, kill, update_agent, anyone else). Container/incarnation rollback on bail. Regression tests: `TestRestartAgentRespectsConcurrentArchive`, `TestRestartAgentRespectsConcurrentKill`.
- **`kill_agent` migrated** (`ceb0bb2`): both def-write paths (no-live-incarnation fast path + post-stop path) use `Update(revision)`; CAS conflict returns `BAD_REQUEST` naming the race.

## What's still owed

### 1. `archive_agent` — same shape, same race

`pkg/rpc/handlers/archive.go` does the read-modify-write pattern. If a concurrent `restart_agent` commits between archive's initial Get and final Put, archive can clobber restart's `lifecycle=running` with `lifecycle=archived` — except the operator already had to archive a stopped agent, so the more realistic race is the symmetric `restart_agent` resurrecting an archive (which IS covered by restart's CAS).

Still worth migrating archive for symmetry + defense-in-depth: the operator's intent in archive is explicit ("this agent is dead, release the name"), and concurrent `update_agent` editing Description shouldn't be silently lost.

Fix shape (mirrors `kill_agent`'s migration in `ceb0bb2`):

```go
defEntry, err := deps.Definitions.Get(...)
// capture revision
initialDefRevision := defEntry.Revision()
// ... existing logic, build the archived def ...
raw, _ := json.Marshal(def)
if _, err := deps.Definitions.Update(ctx, key, raw, initialDefRevision); err != nil {
    if errors.Is(err, jetstream.ErrKeyExists) {
        return emitErr(emit, sextantproto.ErrCodeBadRequest,
            fmt.Sprintf("agent %s definition changed during archive; re-issue if appropriate", id))
    }
    return emitErr(emit, sextantproto.ErrCodeInternal, ...)
}
```

### 2. `update_agent` (when it lands)

There's no `update_agent` handler today, but the design space includes mutable fields (Description, EscalateTo, Runtime tweaks). When it ships, it MUST use CAS — otherwise it races with restart/kill/archive on every write.

### 3. `restart_agent`'s mid-flight error-path Puts

restart.go lines 241–244 + 269–272 write `def.Lifecycle = LifecycleDefined` in error paths via plain `putJSON`. These run on the failure-rollback path and aren't strictly raceable in normal operation (they happen before the CAS loop is entered), but for consistency they should also use CAS. Low priority; file as a sub-item if you want full discipline.

### 4. Spawn — no migration needed

`spawn_agent` writes the def for the first time (`Update` would fail with ErrKeyNotFound). Stays on `Put`. Documenting here so future readers don't add CAS there by mistake.

## Pattern for the implementation

Follow `kill_agent`'s shape verbatim:

1. After the initial `deps.Definitions.Get(...)`, store `initialDefRevision := defEntry.Revision()`.
2. Build the mutated def as before.
3. `json.Marshal` the def.
4. Call `deps.Definitions.Update(ctx, key, raw, initialDefRevision)`.
5. On `errors.Is(err, jetstream.ErrKeyExists)`, return `BAD_REQUEST` with a message naming the race + suggesting re-issue.
6. On any other error, return `INTERNAL`.
7. If the handler has side effects (container stops, incarnation Puts), rollback those before returning on CAS conflict. See restart.go for the rollback shape.

The retry-vs-bail decision: for `restart_agent` we BAIL (because the side effects include a freshly-spawned container that needs cleanup). For handlers without side effects (archive's only effect is the def write), BAIL still makes sense — the operator should re-issue against the now-current state, and silent retry would mask the race.

## Test pattern

`pkg/rpc/handlers/restart_test.go::TestRestartAgentRespectsConcurrentArchive` is the model. Uses `incs.putHook` to inject a concurrent def write at the right moment, asserts the handler refuses to overwrite. For `archive_agent`, the symmetric injection point would be wherever archive's incarnation/def writes happen.

## Acceptance

- `archive_agent` uses `Update(revision)` on its final def write; returns `BAD_REQUEST` on `ErrKeyExists`.
- `TestArchiveAgentRespectsConcurrentRestart` (analog of the restart tests) injects a restart-shaped def write mid-archive and asserts archive refuses to clobber.
- `update_agent` (when it ships) similarly uses CAS — track in the update_agent ticket.
- `make lint-go` stays at 0; existing handler tests still pass against the migrated interface.

## Related

- `pkg/rpc/handlers/spawn.go` — `AgentMutableKV` interface + Update method.
- `cmd/sextantd/spawn.go` — `kvMutableAdapter.Update`.
- `pkg/rpc/handlers/restart.go` — reference migration with rollback on bail.
- `pkg/rpc/handlers/kill.go` — reference migration without rollback.
- `pkg/rpc/handlers/restart_test.go` — `incs.putHook` injection pattern.
- `pkg/sextantd/lifecycle_watcher.go` — the original CAS precedent (CAS + 3-retry budget shape, used because the watcher has no side effects to rollback).
- Codex adversarial-review rounds 4–7 — the chain of races that motivated the sweep.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-handlers-cas-writes.md
Discovered in: Codex adversarial-review rounds 4–7 caught a series of operator-visible races where restart_agent and kill_agent's plain-Put writes could clobber concurrent archive/kill/restart commits. The fixes landed via CAS via jetstream.KeyValue.Update(revision); archive_agent has the same shape and still uses plain Put
Original created_at: 2026-05-27T11:25-07:00
<!-- SECTION:NOTES:END -->

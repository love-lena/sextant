---
id: TASK-31
title: Pay down accumulated lint debt across cmd/ + pkg/
status: Done
assignee: []
created_date: '2026-05-25 19:25'
labels:
  - chore
  - lint
  - tech-debt
  - 'slug:chore-lint-debt-paydown'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

`make lint-go` now exits 0 with zero issues reported. Achieved via:

- **errcheck sweep on `cmd/sextant/*.go`** — routed line-leading `fmt.Fprintf`/`Fprintln` calls through the `output.go` `printf`/`println` wrappers that discard errors explicitly. 86 hits cleared in one mechanical pass (`cbb1c8f`).
- **gofumpt drift** swept via `go run mvdan.cc/gofumpt@latest -w` on the touched files (`dd32a58`, plus several inline fixes in this commit).
- **gosec on test code** excluded globally via a `.golangci.yml` `exclusions.rules` entry — test code's exec/file/cred patterns are overwhelmingly false-positive (test-controlled args, t.TempDir() permissions, hardcoded test creds).
- **Production gosec hits** annotated with `//nolint:gosec` + documented reason (doctor's git invocations use the repoRoot from sextantd config, not user input; preflight's exec uses a whitelist of host-dep binary names; pruner's `os.Symlink` walks daemon-controlled worktrees).
- **contextcheck on `daemon.go:387` and cleanup closures** annotated with `//nolint:contextcheck` + reason (deferred-against-Background cleanups are intentional — outer ctx may be canceled by the time defer runs).
- **staticcheck SA2001** at `spawn_test.go:1242` — deleted the no-op Lock/Unlock leftover from a mid-thought test refactor.
- **staticcheck QF1008** at `tail.go:79` — dropped the embedded `Time` selector.
- **`unused`**: deleted `buildPruneFixture` (no callers), annotated `userLog` as `//nolint:unused` with a pointer at its planned use (output protocol wave).

The companion ticket `chore-cobra-errcheck-debt` is closed alongside — its 78 hits were the bulk of the errcheck sweep above.
---

## Summary

`make lint-go` has a backlog of warnings that don't block correctness but obscure new regressions. Snapshot from 2026-05-25:

| Linter | Count | Files |
|---|---|---|
| gosec (G204/G304/G306/G703) | 15 | `cmd/sextant/doctor.go`, `cmd/sextant/doctor_test.go`, `cmd/sextant/tail.go`-adjacent test code |
| gofumpt | 5 | `cmd/sextantd/spawn_test.go`, `pkg/mcpserver/caller_test.go`, others |
| contextcheck | 3 | `cmd/sextantd/daemon.go:381` (worktree-prune ctx wiring), others |
| staticcheck (SA2001, QF1008) | 2 | `cmd/sextant/tail.go`, `pkg/rpc/handlers/spawn_test.go` |
| unused | 1 | `pkg/worktree/pruner_test.go` (buildPruneFixture) |

Total: 26 issues. Mix of pre-existing and freshly landed.

## Buckets

1. **gosec noise on test code** (~10 of the 15 gosec hits). `exec.Command("git", ...)` with test-controlled args, `os.WriteFile(0o644)` on temp dirs, `os.ReadFile(varPath)` where varPath is a test fixture. Standard fix: `//nolint:gosec // test-controlled args` per the existing repo pattern.

2. **gosec on doctor.go production code** (~5 hits). `exec.Command("git", ...)` for the binary-version + working-tree checks. Already runs against `cfg.Worktree.RepoRoot` — could either suppress with a documented nolint, or refactor to take args via a typed struct so gosec stops complaining.

3. **gofumpt drift** (5 hits). Just need `make fmt` and a commit — but agents have been reverting fmt drift to keep their commits scoped, so it accumulates. Land one "fmt: catch up" commit when nothing else is in flight.

4. **contextcheck on daemon.go:381** (worktree pruner loop). Real concern: `worktreeRT.startPruneLoop(d.supCtx)` may be receiving a non-derived context. Needs investigation — could be a real bug, not just lint noise.

5. **staticcheck QF1008 on tail.go:114** — `msg.Envelope.Ts.Time.Format(...)`. Trivial: drop the embedded field.

6. **staticcheck SA2001 on spawn_test.go:1242** — empty critical section (`vols.mu.Unlock()` with no prior Lock visible to the linter). Investigate, then either fix or document.

7. **unused buildPruneFixture in pruner_test.go** — dead code, delete.

## Acceptance

`make lint-go` exits 0 on main. CI gate can then be re-enabled (if disabled) without a flood of irrelevant findings.

## Related

- [[feat-doctor-stale-binary-detection]] — `doctor.go`'s git invocations (bucket 2) were introduced here.
- [[feat-daemon-lifecycle-ergonomics]] — A5's `doctor_test.go` additions are bucket 1.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/chore-lint-debt-paydown.md
Discovered in: post-merge of feat-daemon-lifecycle-ergonomics — `make lint-go` was already failing before the merge and stayed failing after; want a clean baseline so future PRs can re-enable lint as a merge gate
Original created_at: 2026-05-25T19:25-07:00
Resolved at: 2026-05-27T03:10-07:00
<!-- SECTION:NOTES:END -->

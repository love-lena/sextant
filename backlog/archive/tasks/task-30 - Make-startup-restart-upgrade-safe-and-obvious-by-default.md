---
id: TASK-30
title: Make startup / restart / upgrade safe and obvious by default
status: Done
assignee: []
created_date: '2026-05-25 18:16'
labels:
  - feature
  - ergonomics
  - sextantd
  - doctor
  - init
  - ops
  - 'slug:feat-daemon-lifecycle-ergonomics'
  - P2
  - 'closed:resolved'
dependencies: []
priority: medium
ordinal: 30000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

All five sub-items landed during the 2026-05-25/26 bootstrap-onboarding push. Mapping to commits:

1. **`init` clarity** — `cmd/sextant/init.go` ships `--check` (read-only dry-run), a single-line summary, and outcome-tagged checks (`outcomeOK` / `outcomeWouldWrite` / `outcomeWouldError`). Commit `df258a6`, merged via `e85c370`.

2. **`sextantd` always-on log** — daemon tees output to `<data_dir>/sextantd.log` on every start. Commit `31c5fe9`, merged via `cd26720`.

3. **`sextantd` double-start guard + `--restart`** — daemon refuses to start when a healthy one is up, cycles cleanly with `--restart`. Commit `96f408f`, merged via `91ba592`.

4. **`sextant start/stop/restart/status/logs`** — operator-facing wrappers shipped in `cmd/sextant/{start,stop,restart,status,logs}.go` as part of the bootstrap track.

   4a. **Zombie / orphan detection** — `findOrphanSextantd` in `cmd/sextant/daemon_lifecycle.go` scans the process table for full-path matches and refuses start / sweeps on stop. Tests in `daemon_lifecycle_test.go`.

5. **`doctor` remedies** — `cmd/sextant/doctor.go` annotates failing rows with `Fix:` lines and tags each check with a stable `remedy_id` for `--json` consumers. Commit `f5d6939`, merged via `bd9ef0d`.

The five PRs landed as separate worktree merges from the parallel implementation push; the full audit trail is in `plans/feat-daemon-lifecycle-ergonomics-impl/` if a reader needs the per-track context.

Cobra migration ([[feat-cli-cobra-fang-migration]]) will re-shape some of these surfaces (`sextant start` → `sextant daemon start` per [[feat-cli-resource-verb-cleanup]]), but the underlying capability set this ticket asked for is in place.

## Summary

The first-five-minutes lifecycle surface (`sextant init` → `sextantd` → `sextant doctor` → recovery on breakage) works, but every step has a small ergonomic cliff that compounds into "operator has to read source or ask Claude to figure out what state things are in." This issue bundles those cliffs so they can be fixed as one ergonomics pass rather than scattered drive-bys.

## Problems

### 1. `sextant init` idempotency is real but invisible

`cmd/sextant/init.go:16-17` already does the right thing — re-running skips files that exist. But the output doesn't make that obvious; an operator who isn't sure whether `init` is safe to rerun has to read the source or risk `--force` (which **does** regenerate the CA and invalidate JWTs). Need either (a) a clear "all 7 steps already satisfied — nothing changed" summary line, or (b) a `--check` / dry-run mode that reports what would happen without writing.

### 2. `sextantd` doesn't write its own log file

Today `sextantd` logs only to stderr (`cmd/sextantd/main.go` — single `log.Printf`, no file handle setup), so unless the operator pipes it themselves the output vanishes. It should always persist a log at a canonical path (e.g. `~/.local/share/sextant/sextantd.log`), rotated or at least append-safe, so `sextant doctor` and post-mortem debugging always have something to point at. Stderr can keep going to the terminal when running in the foreground; the file write is additive.

### 3. No `sextant`-side wrapper for daemon lifecycle

`sextantd` is the supervisor — it should stay a simple foreground process. But operators shouldn't have to know that. Add `sextant` subcommands that wrap the lifecycle so the daemon stays as-is:

- `sextant start` — double-fork / detach `sextantd`, redirect to the canonical log, wait until `runtime.json` lands, print `daemon up (pid N, log: …)`.
- `sextant stop` — read `runtime.json`, send SIGTERM, wait for graceful shutdown.
- `sextant restart` — stop + start.
- `sextant status` — current PID, uptime, subprocess PIDs (NATS / ClickHouse / shipper), log path. Basically `agents list`-style but for the daemon itself.
- `sextant logs [--follow] [--tail N]` — convenience tail on the canonical log.

This collapses problems 2, 3, and 5 below into one clean operator surface — and leaves the door open for a launchd plist later that just calls `sextant start` under the hood.

### 4. `sextantd` has no double-start guard

Running `sextantd` when one is already up just crashes on port-bind collisions (and now you have a confusing log file and possibly half-started subprocesses). Desired behavior:

- On startup, check for `runtime.json` and probe the control socket / PID.
- If a healthy daemon is already running: print `sextantd already running (pid N, uptime T) — use --restart to replace`, exit 0.
- If `runtime.json` is stale (PID dead): clean up and start normally, log the cleanup.
- `--restart` flag does a graceful SIGTERM → wait → start cycle so operators never have to hunt PIDs.
- `sextant start` (above) inherits this guard for free.

### 4a. Zombie detection: runtime.json absent but sextantd still alive

(Discovered live during the 2026-05-25 ops session — added after the original five.)

`runtime.json` is the canonical "is the daemon up?" record, but it can disappear without the daemon dying: a partial shutdown, a stray `rm`, a test harness that cleaned up the file but not the process. In that state the double-start guard (#4) sees no `runtime.json` and lets the new `sextantd` spawn — which then crashes on the ClickHouse data-dir lock, leaving the operator with two half-broken daemons and no idea what's running.

Fix shape:

- `sextant start` and `sextant stop` resolve the canonical sextantd binary path (via the existing `findSextantdBinary`), then scan the process table for any process whose argv[0] matches that path. We match by full path so unrelated "sextantd"-named binaries elsewhere on `$PATH` don't trigger false positives.
- `sextant start`: if any orphans are found, refuse with `found orphan sextantd process(es): pid N — run 'sextant stop'`. Don't auto-kill; let the operator confirm the action.
- `sextant stop`: always runs the orphan sweep after handling `runtime.json`, SIGTERMs every matching PID, and waits for them to exit. This makes `stop` the universal cleanup the operator can reach for, regardless of how the state got broken.
- Acceptance: `TestFindOrphanSextantd_*` covers the scanner; `TestStart_RefusesWhenOrphanDetected` covers the start refusal; `TestStop_CleansUpOrphanWithoutRuntimeJSON` covers the stop sweep.

### 5. `sextant doctor` should suggest fixes when the answer is obvious

Doctor today reports state; it doesn't tell the operator what to do about it. Easy wins:

| Check failure | Suggested fix to print |
|---|---|
| `daemon not-running` (no `runtime.json`) | `→ start the daemon: sextant start` |
| `binary-version: behind` | `→ refresh installed binary: make install` |
| `working-tree: dirty` against installed SHA | `→ commit/stash, then make install` |
| missing CA / config / creds | `→ run sextant init` |
| NATS reachable but stream missing | `→ restart sextantd to re-run Bootstrap()` |

Format as a trailing `Fix:` line on the failing row, or a "Suggested next steps" block under the table. Keep `--json` output untouched (machines don't need the prose), but tag each check with a stable `remedy_id` so other tooling can consume it.

## Proposed fix shape

One PR per problem, in this order (each useful on its own):

1. **`init` clarity** — add summary line ("N/7 steps already satisfied, 0 written") and a `--check` flag. Tests in `cmd/sextant/init_test.go`.
2. **`sextantd` always-on log** — open `~/.local/share/sextant/sextantd.log` (append, configurable), tee with stderr. Cheap and unlocks every later step.
3. **`sextantd` double-start guard** — pre-startup probe in `cmd/sextantd/daemon.go` before supervisor wiring; add `--restart` flag. Test: start daemon, start second daemon, assert exit 0 + "already running" message.
4. **`sextant start|stop|restart|status|logs`** — operator-facing wrappers over the daemon. `start` does the double-fork and waits for `runtime.json`; `stop` reads PID from `runtime.json` and sends SIGTERM; `status` is `doctor`-lite for just the daemon row; `logs` tails the canonical file. Update `getting-started/first-run.md` to lead with `sextant start` instead of `sextantd &`.
5. **`doctor` remedies** — extend each check's struct with `Remedy string` (empty when none); render below failing rows. Test that known failures emit known remedies.

## Acceptance

- `sextant init` rerun on a complete install: zero files written, single-line summary, exit 0.
- `sextantd` started any way: `~/.local/share/sextant/sextantd.log` exists, last log line matches the most recent stderr line.
- `sextantd` with a daemon already up: exits 0 with "already running (pid …)"; `sextantd --restart` cycles it.
- `sextant start` from a clean state: returns 0 once `runtime.json` lands, daemon survives terminal close, `sextant status` shows the PID, `sextant logs --tail 20` prints the startup banner. `sextant stop` removes `runtime.json` and the process is gone.
- `sextant doctor` with `sextantd` stopped: failing row + `Fix: sextant start`, exit 2.

## Related

- [[feat-doctor-stale-binary-detection.md]] — the stale-binary warn check is the model for "doctor surfaces things"; this issue extends the same idea to "doctor also tells you what to do."
- [[bug-clienttoml-stale-port-on-restart]] — adjacent restart-ergonomics bug; both point at "the daemon lifecycle should be safer to re-run."
- [[feat-make-install-target]] — `make install` is the implied remedy for the stale-binary doctor row above.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-daemon-lifecycle-ergonomics.md
Discovered in: operator session — `conversation` was empty because `sextantd` wasn't running; surfacing the cause and recovery path took longer than it should have
Original created_at: 2026-05-25T18:16-07:00
Resolved at: 2026-05-26T22:35-07:00
<!-- SECTION:NOTES:END -->

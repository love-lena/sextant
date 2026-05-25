# Milestone status

The bootstrap plan is split into 18 milestones (`plans/bootstrap.md`). This page maps each to its implementation state at this snapshot.

| Milestone | Title                                | Status        | Where to look                                   |
|-----------|--------------------------------------|---------------|-------------------------------------------------|
| M0        | Repo scaffold & Go workspace         | ✅ Done        | `go.mod`, `Makefile`, `.golangci.yml`           |
| M1        | Envelope schema & sextant-proto      | ✅ Done        | `pkg/sextantproto/`                             |
| M2        | NATS bootstrap & subject layout      | ✅ Done        | `pkg/natsboot/`                                 |
| M3        | ClickHouse bootstrap & schema        | ✅ Done        | `pkg/clickhouseboot/`                           |
| M4        | sextant-client-go (read path)        | ✅ Done        | `pkg/client/`                                   |
| M5        | Signing CA & sextantd skeleton       | ✅ Done        | `cmd/sextantd/`, `cmd/sextant/{init,doctor}`, `pkg/authjwt/` |
| M6        | sextant-shipper                      | ✅ Done        | `cmd/sextant-shipper/`, `pkg/shipper/`, `pkg/shipperboot/`   |
| M7        | sextant-client-go (write path) + RPC | ✅ Done        | `pkg/rpc/`                                      |
| M8        | @sextant/client (TypeScript)         | ✅ Done        | `clients/typescript/`                           |
| M9        | Sidecar container image              | ✅ Done        | `images/sidecar/`                               |
| M10       | MCP server (sextant tools)           | ✅ Done        | `pkg/mcpserver/`                                |
| M11       | Spawn flow E2E                       | ✅ Done        | `cmd/sextantd/spawn.go`, `pkg/rpc/handlers/spawn.go`         |
| M12       | CLI completes bootstrap surface      | ✅ Done        | `cmd/sextant/`                                  |
| M13       | First TUI                            | ✅ Done        | `cmd/sextant-tui-agents/`                       |
| M14       | Worktree management                  | ✅ Done        | `pkg/worktree/`, `pkg/rpc/handlers/worktree.go` |
| **M15**   | **Switchover readiness**             | ✅ **Done**    | Verified state. Phase 1 ends here; sextant agents now drive development. |
| M16       | Self-update flow                     | ⛔ Not built   | Spec only (`specs/architecture.md` §12). `SIGUSR2` is a log-only stub at `cmd/sextantd/main.go:98-99`. |
| M17       | Test environments                    | ⛔ Not built   | Spec only (`specs/architecture.md` §13). `test_envs` KV bucket exists but is unused.                 |

## What "M15 done" means

Per `plans/bootstrap.md#M15`, the acceptance criteria are a verified *state*, not new code:

- All components healthy and supervised by `sextantd`.
- Test suite passes against the running daemon.
- Manual sanity check: an `assistant` agent can be spawned and responds to a prompt.
- Audit log is being written.
- Worktree create → work → merge flow works end-to-end.
- Operator can dispatch a real dev task to a sextant agent and it completes.

At the snapshot's commit (`73462f3`), every gate has been hit. Sextant agents have been merging fixes on `main` via their own worktrees since `2026-05-24`.

## What's still being worked on inside Phase 1

A handful of fixes and small features have landed in `plans/issues/` since the snapshot was first cut, and a few are still open. Recently landed at the snapshot's commit (`73462f3`):

- Bugs: `bug-claude-seed-readonly-breaks-session-persistence`, `bug-classifier-rm-rf-too-broad`, `bug-classifier-curl-multipipe-bypass`, `bug-clienttoml-stale-port-on-restart`, `bug-initial-prompt-not-forwarded-to-sdk`, `bug-name-resolution-inconsistent-across-agents-verbs`, `bug-kill-doesnt-release-name`, `bug-restart-preserve-session-noop`, `docs-install-via-make-install-not-cp`.
- Features: `feat-sextant-ask-verb` (synchronous prompt CLI verb), `feat-container-ssh-passthrough` (opt-in ssh mount class), `feat-worktree-pruner` (14d archive / 30d delete policy), `feat-doctor-stale-binary-detection`.

For the current open list, check `plans/issues/` directly — the directory rotates faster than this book.

These are not full milestones; they're targeted fixes layered onto the M0–M14 base.

## Beyond M17

`specs/architecture.md` ends with five "headline pillars that remain partially open": UI hackability, viz specs, multi-user auth, multi-host federation specifics, and the agent extension model. All are post-M17 and sextant-driven from here on.

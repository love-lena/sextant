# Changelog

All notable changes to sextant are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

See `CLAUDE.md` § "Versioning + PR policy" for the bump-classification rule
and the path-based scope (when an entry is required vs. when a PR is exempt).

## [Unreleased]

### Changed
- Bump `@anthropic-ai/claude-agent-sdk` 0.3.150 → 0.3.154 in the
  sidecar workspace. Notable upstream changes: parity with Claude
  Code v2.1.153 (0.3.153); fix for stdio MCP servers being
  incorrectly restarted on every reconcile pass (0.3.154); new
  `SessionStart` `reloadSkills` + `MessageDisplay` hook events
  (0.3.152) — sextant doesn't currently consume the hook API.
- Bump `@types/node` 22.19.19 → 25.9.1 in the sidecar workspace.
  Major bump of the Node.js typings package; runtime stays on
  Node 22 per `engines.node`. CI confirms clean tsc build of
  both the sidecar entrypoint and `clients/typescript`.

### Added
- `sextant version` and `sextantd version` subcommands print the binary
  version + git short SHA, populated at build time via `-ldflags` from
  the top-level `VERSION` file.
- `CHANGELOG.md` (this file) + CI gate that fails PRs touching
  bump-required paths without a changelog entry.
- `pkg/version` package exposing `Version` and `Commit` vars (defaults:
  `dev` / `unknown` for `go run` paths).

### Changed
- `pkg/sextantproto/doc.go::ProtoVersion` realigned to `0.2.0` to track
  the binary version.
- TypeScript client (`clients/typescript/src/envelope.ts`) `PROTO_VERSION`
  realigned to `0.2.0`.

### Fixed
- **`kill_agent` retries on CAS conflict** —
  [[bug-kill-agent-cas-flakes-integration-tests]]. The kill handler's
  final def-write now retries up to 3 times against concurrent
  legitimate writers (the daemon's L2 reconciler and lifecycle
  watcher) instead of bailing immediately on every CAS conflict.
  Container Stop runs exactly once before the retry loop — kill_agent
  alone among the CAS-migrated handlers retries because its side
  effect is idempotent in practice (a stopped container can be
  stopped again as a no-op), while restart_agent / archive_agent
  keep their BAIL-with-rollback shape. The retry budget mirrors
  `lifecycle_watcher.go`'s `watcherCASRetries`. Fixes the six
  `cmd/sextantd` integration-test flakes
  (`TestM12CLIBinaryWalkthroughAcceptance`, `TestM12CLIWalkthroughAcceptance`,
  `TestSidecarSDKDriverMockRoundTrip`, `TestSidecarSDKDriverMockErrorPath`,
  `TestM11SpawnFlowAcceptance`, `TestAgentCanEditWorkspaceFile`).
- **Reconciler-quiet daemon-test harness** — `startDaemonHarness` now
  writes `reconcile_on_startup = false` into the test config so the L2
  reconciler can't race operator-driven kill / restart CAS writes
  under the integration matrix. Belt-and-suspenders to the kill_agent
  retry budget above; production reconciler behavior is unchanged.
- **`cmd/sextantd` CLI-binary walkthrough** — `TestM12CLIBinaryWalkthroughAcceptance`
  now decodes `--json` output through the `pkg/cliout` envelope
  wrapper (added in commit `e916508`) and gates the destructive
  `agents stop` call behind `--yes`. The test had drifted from the
  CLI surface; without these fixes the test failed before the
  kill_agent flake even had a chance to fire.
- **Sidecar `@sextant/client` resolution on fresh clone** — `make
  lint-sidecar` and `make test-sidecar` no longer fail with `Cannot
  find module '@sextant/client'`. The dependency is now wired through
  an npm workspace at the repo root (`clients/typescript` +
  `images/sidecar/entrypoint`), replacing the dangling
  `node_modules/@sextant/client → ../../../client-ts` symlink that
  pointed at a directory that doesn't exist in the repo. The sidecar
  image build inlines an equivalent workspace root so the container
  layout stays self-contained.

## [0.2.0] — 2026-05-28

First tagged baseline. Establishes the version surface that prior
untagged releases (`v0.1.x` informal) lacked.

### Added
- **Agent lifecycle truth** — heartbeat cache, startup reconciler,
  container watcher (PR #2, PR #3). `lost` transition added as a
  fourth terminal state; `LifecyclePayload.source` field carries the
  reporter discriminator.
- **Chat TUI lifecycle word** — header renders the lifecycle state
  next to the existing color-coded dot (PR #9), with relative-time
  suffix on terminal states (`ended (12m ago)`).
- **Chat TUI restart error banner** — inline banner surfaces
  `restart_agent` RPC failures when the lost-state TUI has input
  disabled (PR #10).
- **`agents check` heartbeat secondary signal** — `get_agent_status`
  extended with `IncludeHeartbeat` flag; `agents check` returns
  `degraded` when lifecycle is `running` but the heartbeat is stale
  (PR #11).

### Changed
- **CLI verb migration** (PR #12, breaking-compatible via aliases):
  `agents spawn → agents create`, `agents kill → agents stop`,
  `audit query → audit list`, `worktree destroy → worktree delete`.
  Old verbs continue working as aliases for one release.
- Conventions doc adopts the closed-exception verb-vocabulary list
  (`restart`, `archive`, `prompt`, `answer`, `defer`, `escalate`,
  `tail`, `merge`, `diff` allowed as exceptions to default CRUD).

### Fixed
- **`archive_agent` CAS write** (PR #8) — prevents concurrent
  `restart_agent` / `kill_agent` / `update_agent` from clobbering an
  archive's def commit. Completes the handler-CAS sweep started for
  `restart_agent` and `kill_agent`.
- **Nilaway false positives in `pkg/tui/chat/`** (PR #7) — explicit
  slice init in `wordWrap` / `wrapWithFirstWidth` / `FramesToTurns`;
  guard on `lastAgentTurnIndex`. CI gate for `make lint-nilaway`
  restored (was silently disabled by a prior install step).

### Internal
- 7 PRs merged in a single dispatch session on 2026-05-27/28 (PRs #6
  through #12). Documented in `plans/issues/` with deferred /
  resolved tickets cross-linked.

[Unreleased]: https://github.com/love-lena/sextant/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/love-lena/sextant/releases/tag/v0.2.0

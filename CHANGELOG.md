# Changelog

All notable changes to sextant are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

See `CLAUDE.md` § "Versioning + PR policy" for the bump-classification rule
and the path-based scope (when an entry is required vs. when a PR is exempt).

## [Unreleased]

### Added
- **`sextant agents context <agent> -i` (Phase B)** — the raw-context
  view's interactive TUI (`pkg/tui/contextview`): a scrollable, tailing
  `StreamViewport` over the agent's SDK session JSONL with mode keys 1–6
  (raw/conversation/tools/thinking/usage/tree). The per-line rendering +
  mode vocabulary moved into `pkg/sessionlog` (`Mode` / `RenderLine` /
  `ParseMode`), so the CLI dump and the TUI render identically (DRY).
  Self-registers for `sextant tui` / `sextant dash`. Completes
  `plans/issues/feat-agents-context-view.md`.
- **`sextant traces show <id> -i`** — interactive span-tree explorer
  (`pkg/tui/traces`): a collapse/expand outline (j/k nav, Enter toggles,
  Esc collapses) over a `query_trace` result, built on `widget.ListPane`
  fed a flattened depth-annotated row slice. The static `traces show`
  stdout renderer now shares the same `BuildSpanTree` / `FlattenVisible`
  projection (DRY). Self-registers for `sextant tui` / `sextant dash`.
  Resolves `plans/issues/feat-tui-traces-component.md`.
- **`sextant pending list -i` + dash pending pane** — the pending-requests
  TUI now exists (`pkg/tui/pending`): a live `ListPane` of unanswered
  user_input requests with j/k nav, `/` filter, and Enter emitting an
  answer intent, built on the P0 widget toolkit. Self-registers, so the
  `sextant dash` pending pane (previously a placeholder) and the
  `sextant tui` menu pick it up automatically. NOTE: nothing in production
  publishes input-requests yet, so the surface is empty against a live
  daemon until an escalation producer lands (RFC §6 / Open Q5). Resolves
  `plans/issues/feat-tui-pending-component.md`.
- **`pkg/tui/widget` shared TUI toolkit** — the widget layer the
  interactive-surface workstream composes: `ListPane[T]` (generic cursor
  list with nav / selection / `/`-filter / scroll-window), `StreamViewport`
  (scrollback over `bubbles/viewport` with tail-follow + ring-buffer cap +
  `g`/`G`), `DetailPane` (label/value sections), and the `Source[T]` /
  `Pump` data adapter (`SubscribeSource` / `TailSource` / `OnceSource`).
  Internal foundation; no operator-visible change on its own. Per the RFC
  `plans/rfc-tui-workstream.md` (P0).

### Changed

### Fixed
- **Sidecar `version` reported a stale hard-coded string** —
  [[bug-sidecar-version-string-stale]]. The `version` command printed
  `sextant-sidecar 0.2.0` while `package.json` (and the MCP
  client-identity handshake) said `0.1.0`. Both call sites now read the
  version from `package.json` at runtime via a new `src/version.ts`
  (`SIDECAR_VERSION`), so they can't drift from the manifest or each
  other; a test pins the invariant.

## [0.3.0] — 2026-05-28

Interactive surfaces (`tui`, `dash`, `agents context`) plus the
version-observability tooling that this release-cut workflow itself
relies on. MINOR bump: everything below is additive — no verb, flag,
output format, or wire shape was removed.

### Added
- **`sextant dash` flagship multi-pane TUI** — composes registered
  Tier 1 components into a Stickers flex layout with BubbleZone
  mouse click regions. Default pane layout is embedded as
  `dash-default-config.toml`; `~/.config/sextant/config.toml`
  overrides when present. `sextant dash --dump-default-config`
  prints the embedded default. Tab / Shift+Tab cycles focus;
  number keys + mouse click also work. Inter-pane routing via
  the existing `OpenMsg` / `LoadMsg` component convention.
  Pending pane is a placeholder until `feat-tui-pending-component`
  lands.
- **`sextant tui` Huh-driven discovery menu** — lists every Tier 1
  component registered via `pkg/tui/component`'s registry and
  launches the corresponding `-i` surface on selection. New
  components appear automatically as they self-register via
  `init()`.
- **`sextant agents context <agent>` (Phase A)** — operator surface
  for inspecting an agent's SDK session in raw form. CLI dump +
  `--follow` (tail) + `--mode=<raw|conversation|tools|thinking|usage|tree>`
  filters. New `pkg/sessionlog` typed JSONL parser underlies the view
  modes. Daemon bind-mounts a per-agent `<data-dir>/agents/<uuid>/claude-projects/`
  host directory at `/home/agent/.claude/projects/` inside the container
  so the SDK's session writer ends up writing to a path the host can
  read directly. `get_agent_status` surfaces the projects-dir path +
  current session_id via the new `SessionLogInfo`. Verb `context`
  added to the closed-exception list in `conventions/tui-conventions.md`.
  `-i` TUI mount is a follow-up (depends on `feat-cli-iflag-tier1-components`).
  See `plans/issues/feat-agents-context-view.md`.
- **`sextant doctor` daemon version surface** — `doctor` now queries
  a new `get_version` RPC and prints CLI + daemon version, proto
  version, daemon PID, and start time. Warns when CLI and daemon
  versions diverge (the common case after `make install` without
  a daemon restart).
- **TTY interactive confirm for destructive verbs** — `agents stop`,
  `agents restart`, `agents archive` (incl. `--all-dead`), `daemon
  stop`, `daemon restart` now render a `huh.NewConfirm` prompt when
  stdin is a TTY and neither `--yes` nor `--dry-run` is set.
  Non-TTY callers still get the existing `--yes`-required error.
- **Tier 1 `-i` / `--tui` flag + component registry** — `sextant
  agents list -i` and `sextant agents show <id> -i` launch the
  existing agents TUI inline; `sextant pending list -i` and
  `sextant traces show <id> -i` accept the flag but surface a
  clear "not yet implemented" pointer at the follow-up tickets
  ([[feat-tui-pending-component]], [[feat-tui-traces-component]]).
  New `pkg/tui/component` registry (`Register` / `List`)
  underpins the wiring; each component package self-registers via
  `init()` (`pkg/tui/chat`, `pkg/tui/agents`). The legacy
  `cmd/sextant-tui-agents/` binary now wraps `pkg/tui/agents` as a
  thin standalone.
- `sextant version` and `sextantd version` subcommands print the binary
  version + git short SHA, populated at build time via `-ldflags` from
  the top-level `VERSION` file.
- `CHANGELOG.md` (this file) + CI gate that fails PRs touching
  bump-required paths without a changelog entry.
- `pkg/version` package exposing `Version` and `Commit` vars (defaults:
  `dev` / `unknown` for `go run` paths).

### Changed
- **Protocol version → `0.3.0`** — `pkg/sextantproto.ProtoVersion`
  and the TypeScript client's `PROTO_VERSION` both advance to track
  the binary semver. The wire surface changed additively this cycle
  (new `get_version` RPC; new optional `session_log` field on the
  `get_agent_status` response), so the bump is informational, not a
  break. (A follow-up will split the proto version onto its own line
  — see CLAUDE.md § "Versioning + PR policy".)
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
- Bump `typescript` 5.6.3 → 6.0.3 across the workspace. Major
  compiler upgrade; both `clients/typescript` and the sidecar
  entrypoint compile clean under TS 6 with no diagnostics. The
  99-test sidecar vitest suite + 19-test clients/typescript
  suite both pass. (Build-tooling major; not an operator-facing
  change, so no MAJOR bump of the binary.)

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

[Unreleased]: https://github.com/love-lena/sextant/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/love-lena/sextant/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/love-lena/sextant/releases/tag/v0.2.0

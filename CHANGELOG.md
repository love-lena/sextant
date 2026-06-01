# Changelog

All notable changes to sextant are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

See `CLAUDE.md` § "Versioning + PR policy" for the bump-classification rule
and the path-based scope (when an entry is required vs. when a PR is exempt).

## [Unreleased]

### Added
- **Lost agents now self-heal.** The reconciler grew a recovery branch
  (control-plane RFC §5.3, §8): when a `desired=run` agent is observed
  `lost` or `crashed`, the daemon auto-restarts it via the single-source
  actuator instead of leaving it parked — closing the "noticed it was
  lost and wrote that down" gap. Recovery is governed by a per-agent
  **`RestartPolicy`** (`Always` / `OnFailure` / `Never`, default
  `OnFailure` — a clean exit is not restarted) and the full safety rails:
  exponential backoff (10s ×2, cap 300s), a stable-run backoff reset
  (10 min continuous), a crash budget (5 restarts / 10 min → the agent
  parks in terminal `crashed`), a SIGTERM→30s→SIGKILL grace, and a
  **liveness** probe (3 consecutive missed heartbeats / 10s) that catches
  a wedged-but-still-running agent a container `die` never fires for. All
  timing runs off an injected clock, so the schedule is deterministic in
  tests. Operator surface: `sextant agents list` gains a **`RESTARTS`**
  column and `agents show` reports the restart count + last-exit reason
  (both also on the `list_agents` / `get_agent_status` wire payloads).
- **The wire contract is now generated end-to-end, and a CI gate guards
  it.** The `payloads.go → schemas/*.json` generator (`cmd/sextantproto-gen`)
  also emits `schemas/wire.json` — a machine-readable manifest carrying the
  proto version, the new `WireEpoch` compatibility key, and the closed
  envelope / address / frame enums — and now drives the TypeScript codegen
  too (`go generate ./...` regenerates both the Go schemas and the TS types
  + `proto_version.ts`). `sextantproto.WireEpoch` (Go) / `WIRE_EPOCH` (TS)
  is the single source of truth for the bus compatibility epoch (RFC §5.8).
  A new `schema-compat` CI check (sibling to `changelog entry required`,
  backed by `cmd/sextant-schema-compat` + `pkg/wirecompat`) regenerates the
  schemas, asserts the committed copy is in sync, and **fails the build if a
  breaking wire change — removed/renamed field, type change,
  optional→required, removed enum value — lands without a `WireEpoch` bump.**
  A checked-in Go↔TS message corpus (`pkg/sextantproto/testdata/wire-corpus`)
  is decoded by the generated TS types so the two ends can't silently drift.
- **Every agent container now carries a spec-fingerprint label**
  (`sextant.spec_fingerprint`): a deterministic hash of the image, the
  ordered mount targets, and the sorted env-var key set, stamped at
  build time by the single-source container-spec builder. It is
  identity-independent — a restart of an unchanged definition reproduces
  the same fingerprint — which is the seed for control-plane drift
  detection and converge-by-restart (RFC §5.6). No operator-facing
  behavior change beyond the new label; it's inert until the reconciler
  reads it.

### Changed
- **`sextantd` is now a declarative control plane: handlers write
  desired state, a single reconciler is the sole actuator.** `spawn`,
  `stop` (the `kill_agent` verb), and `archive` no longer touch Docker
  — they write `spec.desired` (`run` / `paused` / `archived`) to KV and
  enqueue a reconcile; `restart` bumps `spec.reactuation_nonce` to
  request a fresh incarnation of the same desired state. One
  level-triggered reconcile loop — fed by a work queue, a 30–60s
  periodic sweep, and sensor events treated strictly as *hints* (never
  the source of truth) — is the only thing that builds + runs + stops
  containers (via the single-source `buildAgentContainerSpec`) and the
  only writer of `status.observed`. The `AgentDefinition` record is
  split into `spec` (operator/desired intent: `desired`, `runtime`,
  `sandbox`, `generation`, `reactuation_nonce`, …) and `status`
  (reconciler-observed reality: `observed`, `observed_generation`,
  `current_incarnation_id`, …); the old top-level `lifecycle` field is
  gone and `Lifecycle()` is now a derived method projecting spec/status
  onto the legacy strings (RFC §5, §5.2, Appendix C). The
  carried-forward runtime invariants — incarnation-CAS, a terminal
  sidecar status outranking a `lost` reading, and the 5s container-die
  debounce — are unchanged and covered in
  `pkg/sextantd/reconcile_test.go`. `WireEpoch` bumps 1→2: the
  persisted `AgentDefinition` shape changed.

  **Declared breakage (operators):**
  - **Old persisted KV agent records do not auto-migrate.** There is no
    in-place migration; a record written under the pre-split (`v1`)
    shape will not decode into the new `spec`/`status` layout. Operators
    upgrading an existing daemon must perform a **one-time reset** of the
    agents/incarnations KV buckets (drain or delete running agents
    first). This is acceptable at the current pre-1.0 stage; a real
    migration is a separate follow-up if/when one is warranted.
  - **Auto-recovery is still absent: a `lost` agent stays `lost`.** The
    reconciler observes and records loss but does not yet re-actuate a
    crashed/ended incarnation back to its desired `run` state. That
    closed-loop recovery is restored by `feat-ctl-p1-recovery`.
- **The TypeScript client's wire constants are now generated, not
  hand-written.** `PROTO_VERSION`, the `KIND_*` / `ADDRESS_*` / `FRAME_*`
  constant sets, and the new `WIRE_EPOCH` live in a generated
  `clients/typescript/src/proto_version.ts` (sourced from `wire.json`) and
  are re-exported from `@sextant/client`. The public package surface is
  unchanged (plus the new `WIRE_EPOCH` / `FRAME_*` / `ADDRESS_KINDS`
  exports); the previously hand-maintained definitions of these constants in
  `src/envelope.ts` are removed. No external consumers exist today; any code
  importing the constants from the internal `./envelope.js` path (rather than
  the package root) must import from `./proto_version.js`.
- **`spawn_agent` and `restart_agent` now build the container spec
  through one `buildAgentContainerSpec` projection.** Previously each
  handler assembled the mount/env/label set inline, which is how
  `restart` drifted from `spawn` (see Fixed). The spec is now a pure
  projection of the persisted `AgentDefinition` plus the daemon's
  host-environment context; the only spawn-vs-restart differences are
  the freshly-minted incarnation id, the per-incarnation JWT, and the
  session-resume decision — explicit parameters, never the absence of a
  mount. `RestartDeps` gains `Worktree` + `RepoRoot` so restart can
  re-mount the same worktree `/workspace` and the `<repo>/.git` bind
  spawn produced (the lossless-restart prerequisite, RFC §5.4). No
  operator-facing CLI change.
- **`sextant tui` now walks you through arg-requiring surfaces.** Picking
  an agent-scoped surface (chat, agent detail, agents context) prompts an
  agent picker (live `list_agents`, falls back to free text if the daemon
  is unreachable); a trace surface prompts for the trace id. The resolved
  command is printed (`→ sextant agents show <uuid> -i`) so it's easy to
  copy/paste and reuse. `component.Meta` gains `Arg` / `ArgKind` /
  `NoIFlag` to drive this.
- **The RPC surface is now one declarative `VerbSpec` table instead of
  four parallel enumerations.** The verb-name constants, the `CapFor`
  capability mapping, the daemon's staged handler registration, and the
  schema generator's hand-maintained payload list used to enumerate the
  same verbs in four places; adding a verb and forgetting one was a live
  drift class (the generator's type list was the hidden 4th copy). They
  now derive from `rpc.VerbSpecs` (`{name, capability, phase, req, resp}`):
  dispatch registration iterates it per phase, `CapFor` reads it, and
  `cmd/sextantproto-gen` walks its req/resp types. Registration fails
  loudly if a verb lacks a handler or a handler lacks a verb. Pure
  internal refactor — no observable behavior change: every verb keeps its
  exact name and capability, the staged registration order is preserved,
  and `go generate ./...` produces byte-identical schema output (no
  `WireEpoch` bump). RFC §5.8.

### Fixed
- **`restart_agent` silently dropped three mounts `spawn_agent` adds —
  the gitconfig, the worktree `<repo>/.git` bind, and the opt-in SSH
  bind.** A restarted agent lost its git identity (`user.name`/`email`),
  couldn't resolve its worktree's `.git` pointer (so `git status`/commit
  failed), restarted into the M11 stop-gap dir instead of its worktree,
  and — for `mounts = ["ssh"]` templates — lost `git push` auth. Latent
  until now because restart was operator-initiated and rare; it becomes
  load-bearing under the upcoming auto-restart path, where a lossy
  restart would *propagate* the drift on every recovery. The
  single-source `buildAgentContainerSpec` (above) makes restart reproduce
  the full spawn mount set by construction; a docker-backed e2e
  `docker inspect`s both containers and asserts identical mount sets +
  env (modulo incarnation id / JWT). Subsumes RFC §10.3.
- **`restart_agent` dropped the per-agent claude-projects bind-mount —
  the real reason `sextant agents context <agent>` showed nothing.**
  `spawn_agent` bind-mounts `<data>/agents/<uuid>/claude-projects` at
  `/home/agent/.claude/projects` so the SDK's session journal lands on a
  host path the operator can read; `restart_agent` re-attached the
  claude-seed volume but **not** that bind-mount, so a restarted
  incarnation wrote its session inside the container and the host dir
  stayed empty (and `agents context` reported "no on-disk session yet").
  Restart now re-applies the mount (gated on `AgentsDataRoot`, wired via
  `RestartDeps`), with a regression test asserting the restart container
  spec includes it. Operators must restart the daemon + the affected
  agent to pick this up.
- **`sextant agents context <agent>` leaked a raw filesystem error** when
  the agent's per-agent projects dir doesn't exist on disk (agent spawned
  before the context bind-mount, or no SDK turn flushed yet) — the daemon
  reports a `SessionLog` in KV but the dir was never created. The command
  printed `read projects dir …: no such file or directory`; it now returns
  the same friendly, actionable message as the missing-session-file case
  (`agent has no on-disk session yet … prompt the agent then retry`).
  Affects both the CLI dump and `agents context <agent> -i`. Found by
  `sextant agents context assistant` against an agent with a stale KV
  session pointer.
- **`sextant tui` could only launch 5 of its 9 entries; `q`/`esc` didn't
  quit.** Selecting chat / agent-detail / agents-context / traces errored
  (`accepts 1 arg(s)` / `unknown shorthand flag 'i'`) because the menu ran
  `sextant <command> -i` with no positional and no per-surface launch
  rules. The menu now collects the required arg (above), launches chat
  bare (it has no `-i`), and binds `q`/`esc` to quit (huh's default was
  ctrl+c-only, contradicting the menu's own help). Found by manually
  driving every entry through the menu in a PTY.

## [0.5.0] — 2026-05-28

Interactive-surfaces workstream, phase 2: four more `-i` surfaces built
on the P0 widget toolkit — `daemon logs`, `worktree list`, `audit list`,
and the `agents show` detail inspector (the `DetailPane`'s first real
consumer). MINOR — all additive; `agents show -i` now opens the detail
inspector rather than the focused list, but no scripted invocation,
output format, or wire shape changed. Per the RFC `plans/rfc-tui-workstream.md` (P2).

### Added
- **`sextant agents show <id> -i` detail inspector** — `agents show -i`
  now opens a `DetailPane` inspector (`pkg/tui/agentdetail`) instead of
  the focused agents list: lifecycle / template / version / session /
  owning-worktree, assembled client-side from `get_agent_status` +
  `list_agents` + `worktree_list` (no new RPC; degrades gracefully when
  a field is missing — RFC §6 †). Self-registers for `sextant tui`.
  Per the RFC P2.
- **`sextant audit list -i`** — interactive audit-log browser
  (`pkg/tui/auditlist`): a `ListPane` over the `query_audit` RPC (last
  24h; j/k nav, `/` filter, `r` refresh, Enter emits a detail intent).
  Self-registers for `sextant tui` / `sextant dash`. Per the RFC P2.
- **`sextant worktree list -i`** — interactive worktree browser
  (`pkg/tui/worktreelist`): a `ListPane` over the `worktree_list` RPC
  (j/k nav, `/` filter, `r` refresh, Enter emits a diff intent).
  Self-registers for `sextant tui` / `sextant dash`. Per the RFC P2.
- **`sextant daemon logs -i`** — interactive tailing log viewport
  (`pkg/tui/logsview`): a scrollback `StreamViewport` over the daemon log
  file (j/k scroll, g/G top/bottom, tail-follow). A thin composition —
  `StreamViewport` + `widget.TailSource` — demonstrating the P0 widget
  leverage. Self-registers for `sextant tui`. Per the RFC P2.

### Changed
- **Proto version → 0.5.0** to track the binary number (per
  `conventions/versioning.md`). **No wire change this window** — the new
  surfaces are pure front-ends over existing RPCs / subjects / files.

## [0.4.0] — 2026-05-28

Interactive-surfaces workstream, phase 1: a shared TUI widget layer
(`pkg/tui/widget`) plus three new `-i` surfaces composed from it
(`pending list`, `traces show`, `agents context`). MINOR bump —
everything is additive; no verb, flag, output format, or wire shape was
removed. Per the RFC `plans/rfc-tui-workstream.md` (P0 + P1).

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
- **Proto version → 0.4.0** to track the binary number (per
  `conventions/versioning.md`, proto tracks the binary until the
  version-line split lands). **No wire change this window** — the new
  surfaces are pure front-ends over existing RPCs / subjects / files;
  no RPC, envelope field, or payload shape changed.

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

[Unreleased]: https://github.com/love-lena/sextant/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/love-lena/sextant/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/love-lena/sextant/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/love-lena/sextant/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/love-lena/sextant/releases/tag/v0.2.0

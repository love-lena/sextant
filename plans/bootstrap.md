# Bootstrap plan — initial

This is the master plan for building sextant initial from nothing. Classic Claude Code (CLI mode) reads this top-to-bottom and implements each milestone in order.

**Required reading first**: [`../specs/architecture.md`](../specs/architecture.md), [`../conventions/STYLE.md`](../conventions/STYLE.md), [`../conventions/git-workflow.md`](../conventions/git-workflow.md).

## How to use this plan

- Each milestone is a discrete chunk of work.
- Milestones are sequenced — earlier ones unblock later ones.
- A milestone declares: **goal**, **deliverables**, **spec references**, **acceptance criteria**.
- Detailed specs for each milestone live in [`milestones/`](milestones/) and the referenced files under [`../specs/`](../specs/).
- Implementors should fill in per-milestone detail under `milestones/MN-name.md` before implementing if the spec is sparse.

## Phases

- **Phase 0 (Foundation)**: repo scaffolding, conventions, shared types — M0–M3
- **Phase 1 (Infrastructure)**: NATS, ClickHouse, signing CA, sextantd skeleton — M4–M6
- **Phase 2 (Libraries)**: client libs in Go and TS — M7–M8
- **Phase 3 (Agent runtime)**: sidecar image, MCP server, spawn flow — M9–M11
- **Phase 4 (UX)**: CLI, first TUI — M12–M13
- **Phase 5 (Bootstrap completion)**: worktrees, switchover readiness — M14
- **Phase 6 (Switchover)**: first sextant-driven dev task — M15
- **Phase 7 (Self-improvement)**: self-update flow, watchdog, test environments — M16–M17

The **switchover** at M15 is the headline milestone. Before it: classic CC drives. After it: sextant drives.

---

## Milestones

### M0 — Repo scaffolding & Go workspace

**Goal**: working Go workspace with linting, formatting, CI gates.

**Deliverables**:
- `go.mod` at repo root, module path `github.com/love-lena/sextant-initial`
- Repo layout (normative — every later milestone references these paths):
  - `cmd/sextant/` — operator CLI binary (M5 ships `init` + `doctor`; M11/M12 ship remaining verbs)
  - `cmd/sextantd/` — daemon binary
  - `cmd/sextant-shipper/` — shipper binary (M6)
  - `cmd/sextant-sidecar/` — sidecar entrypoint (M9; Go-side parts may land earlier)
  - `cmd/sextant-tui-agents/` — first TUI (M13)
  - `pkg/sextantproto/` — shared envelope/event types (M1)
  - `pkg/natsboot/` — NATS bootstrap (M2)
  - `pkg/clickhouseboot/` — ClickHouse bootstrap (M3)
  - `pkg/client/` — Go client library (M4)
  - `pkg/authjwt/` — JWT issuance helpers (M5)
  - `pkg/shipper/` — shipper logic (M6)
  - `pkg/rpc/` — RPC dispatch + handlers (M7)
  - `pkg/mcpserver/` — MCP server (M10)
  - `pkg/worktree/` — worktree management (M14)
  - Additional `pkg/` subpackages as milestones require, each placed under the relevant binary's logical scope.
- `.golangci.yml` with strict config (`govet`, `staticcheck`, `errcheck`, `gosec`, `revive`, `gocritic`, `nilaway`, `gofumpt`, `goimports`, `unused`, `ineffassign`, `errorlint`, `bodyclose`, `contextcheck`, `copyloopvar`)
- `Makefile` with `lint`, `test`, `build`, `fmt` targets (and `sidecar-image` added in M9). Prefer plain `make` over `taskfile.yml`.
- CI config at `.github/workflows/ci.yml` running `make lint test` on every push; failure blocks merge.
- `.editorconfig`, `.gitignore` for Go projects

**Spec references**: [`conventions/STYLE.md`](../conventions/STYLE.md)

**Acceptance**: `make lint test` exits 0 on an empty workspace with a smoke-test package.

---

### M1 — Envelope schema & sextant-proto

**Goal**: shared types for bus envelopes, agent definitions, events, RPCs.

**Deliverables**:
- `pkg/sextantproto/` (or equivalent) with Go structs for:
  - `Envelope` (the universal wrapper: id, ts, proto_version, from, to, trace_id, span_id, kind, payload)
  - `AgentDefinition`, `AgentIncarnation`, `LifecycleState`
  - `IrEvent` variants (AgentFrame, Lifecycle, Telemetry, Audit, RPC)
  - OTel-shaped types: `Span`, `Metric`, `LogRecord` (matching OTel data model, no SDK dep)
- JSON marshaling tested round-trip
- JSON Schema generation (`invopop/jsonschema` or similar) wired into `go generate`
- Generated JSON Schema files committed under `pkg/sextantproto/schemas/`

**Spec references**: [`specs/protocols/envelope-schema.md`](../specs/protocols/envelope-schema.md)

**Acceptance**: full round-trip tests for every type; `go generate` produces JSON Schemas; schemas check into git.

---

### M2 — NATS bootstrap config & subject layout

**Goal**: deterministic NATS setup that sextantd spawns and manages.

**Deliverables**:
- Helper package `pkg/natsboot/` that:
  - Generates a NATS config file with the two listeners specified in `specs/components/nats.md` (Unix socket: no auth; TCP localhost: JWT required, signed by the sextant CA).
  - Starts `nats-server -js` as a subprocess
  - Waits for ready, returns connection details
  - Creates all required JetStream streams (one per subject hierarchy) with appropriate retention
  - Creates all required NATS KV stores
- **CA dependency**: M2 does not require the sextant CA. The TCP listener is configured to verify JWTs against a CA public-key file path, but the file is allowed to be empty/missing at this stage — agent-side connections are not yet exercised. M5 populates the CA; M11 issues the first agent JWT.
- A standalone `cmd/sextant-natsboot/` for testing
- Test that exercises full bootstrap → connect → publish → consume → teardown over the Unix socket listener

**Spec references**: [`specs/components/nats.md`](../specs/components/nats.md), [`specs/protocols/bus-subjects.md`](../specs/protocols/bus-subjects.md)

**Acceptance**: NATS comes up, all streams/KV exist, can roundtrip a message over the Unix socket listener.

---

### M3 — ClickHouse bootstrap & schema

**Goal**: deterministic ClickHouse setup with schema migrations applied.

**Deliverables**:
- Helper package that:
  - Generates a ClickHouse config from sextant config
  - Starts `clickhouse-server` as a subprocess
  - Applies migrations from `pkg/clickhouseboot/migrations/` on startup
- Initial schema with tables for: events, telemetry traces, telemetry metrics, telemetry logs, audit, agent definition history
- Migration tool: directory-based numbered SQL files, idempotent re-runs
- Test: bootstrap → migration → insert → query → teardown

**Spec references**: [`specs/components/clickhouse.md`](../specs/components/clickhouse.md)

**Acceptance**: ClickHouse comes up, schema applied, can insert and query.

---

### M4 — sextant-client-go (read path)

**Goal**: Go client library for subscribing to the bus and querying history.

**Deliverables**:
- `pkg/client/` exposing:
  - `Client.Connect(configPath)` → loads `~/.config/sextant/client.toml`
  - `Client.Subscribe(subject, from_seq?)` → typed event stream
  - `Client.Query(filter, timeRange)` → past events via ClickHouse RPC (stub until M6)
  - `Client.WatchKV(key)` → live updates from NATS KV
- Reconnection / retry policies built in
- Auth integration: Unix-perm in initial (just read the socket); JWT-ready interface for v2

**Spec references**: [`specs/components/client-libraries.md`](../specs/components/client-libraries.md)

**Acceptance**: a tiny demo program can subscribe and print frames against a running NATS.

---

### M5 — Signing CA & sextantd skeleton

**Goal**: sextantd as a daemon that owns the CA, supervises NATS + ClickHouse, exposes a control RPC surface.

**Deliverables**:
- `cmd/sextant/` binary (operator CLI) with at minimum:
  - `sextant init` subcommand: generates CA keypair at `~/.config/sextant/ca.{key,pub}`, writes `sextantd.toml` + `client.toml`, creates data dirs, seeds default templates (see `specs/architecture.md` "Templates"). Idempotent re-runs.
  - `sextant doctor` subcommand: health diagnostics (NATS up, ClickHouse up, config valid, CA present). Used by M5's smoke test and downstream milestones.
  - (Other verbs deferred to M12; the binary exists here to host `init` and `doctor`.)
- `cmd/sextantd/` (daemon) with:
  - Main daemon mode: starts NATS, starts ClickHouse, listens on a control socket at `~/.local/share/sextant/sextantd.sock`
  - Component health monitoring + restart on failure
  - Signal handling: SIGTERM (graceful shutdown), SIGUSR2 (self-update execv handoff — stub for M16)
- Per-agent JWT issuance helpers (`pkg/authjwt/`). Issuance flow plumbed but no agent consumes it until M11.
- Operator authority: Unix file perms on `~/.config/sextant/` and `~/.local/share/sextant/nats/nats.sock`. No operator JWT.

**Spec references**: [`specs/components/sextantd.md`](../specs/components/sextantd.md), [`specs/cli/commands.md`](../specs/cli/commands.md)

**Acceptance**: `sextant init && sextantd` starts cleanly with NATS + ClickHouse running and healthy; `sextant doctor` reports green.

---

### M6 — sextant-shipper

**Goal**: subscribe to NATS, write to ClickHouse, with at-least-once delivery.

**Deliverables**:
- `cmd/sextant-shipper/` (or a goroutine within sextantd; lean: separate process for failure isolation)
- Per-subject → per-table mapping
- Local buffer (BoltDB or similar) for ClickHouse-unreachable windows
- Dedup via ClickHouse primary key
- Metrics on lag, buffer depth, write rate

**Spec references**: [`specs/components/shipper.md`](../specs/components/shipper.md)

**Acceptance**: events flowing through NATS land in ClickHouse with sub-second lag; shipper survives a ClickHouse restart without event loss.

---

### M7 — sextant-client-go (write path) + RPC

**Goal**: Go client can publish events and call RPC verbs.

**Deliverables**:
- `Client.Publish(subject, event)` with optional reply-to
- `Client.RPC(verb, args, opts)` → typed reply, idempotency key support, timeout
- RPC server side in sextantd: dispatch table, capability checks (stub until §10a JWT lands), audit logging
- Initial RPC verbs implemented: `get_agent_status`, `list_agents`, `read_file` (stub), `query_history`

**Spec references**: [`specs/protocols/rpc-catalog.md`](../specs/protocols/rpc-catalog.md)

**Acceptance**: a tiny demo program can call `list_agents` (returns empty list) and `get_agent_status` (returns 404).

---

### M8 — @sextant/client (TypeScript)

**Goal**: TS library mirroring the Go client API. Used by SDK sidecar (M10) and any TS UI.

**Deliverables**:
- `npm` package `@sextant/client`
- Same primitives as Go client: `connect`, `subscribe`, `query`, `publish`, `rpc`, `watchKV`
- Types generated from JSON Schemas produced in M1
- Built and tested in CI alongside Go

**Spec references**: [`specs/components/client-libraries.md`](../specs/components/client-libraries.md)

**Acceptance**: a tiny TS program can subscribe and call `list_agents` against the same daemon the Go client uses.

---

### M9 — Sidecar container image

**Goal**: the base image every agent's sidecar runs in.

**Deliverables**:
- `images/sidecar/Dockerfile` building `sextant-sidecar:<version>`
- Image contents: Node, Claude Code SDK, `@sextant/client`, sextant sidecar entrypoint, rich tool set (git, gh, jq, ripgrep, fzf, curl, build tools, Go, Node+npm, python+pip)
- Entrypoint: connects to NATS via env-var config, registers the agent, runs the SDK loop, publishes events
- Image build via `make sidecar-image`

**Spec references**: [`specs/components/sidecar-image.md`](../specs/components/sidecar-image.md)

**Acceptance**: image builds; `docker run` starts an interactive shell with all the tools present.

---

### M10 — MCP server (sextant tools)

**Goal**: sextant exposes its tool catalog (§9c) via MCP, sidecar connects, agents can call sextant-specific tools.

**Deliverables**:
- `pkg/mcpserver/` implementing the MCP server protocol
- Tool catalog: communication (`send_message`, `broadcast`), introspection (`list_agents`, `agent_status`, `query_audit`), control (`spawn_agent`, `kill_agent`, `prompt_agent`), system (`emit_event`, `get_metric`)
- Per-call capability check (stub until JWTs land; allows-everything-for-now)
- Sidecar (M9) extended to connect to MCP server and route tool calls through

**Spec references**: [`specs/components/sextantd.md`](../specs/components/sextantd.md) (MCP section), `architecture.md` §9c

**Acceptance**: a sidecar can call `send_message` and the message lands on the destination agent's inbox subject.

---

### M11 — Spawn flow E2E

**Goal**: operator → `sextant spawn <name>` → container running → agent on NATS → first frame published.

**Deliverables**:
- `sextant spawn` CLI verb: takes name, template, host pin
- Sextantd:
  - Looks up template, builds container spec
  - Calls Docker SDK to start container with the right mounts, env, network
  - Registers the agent in NATS KV
  - Issues per-agent JWT (still stubbed)
- Sidecar inside container: connects to NATS, publishes `agents.<uuid>.lifecycle/started`
- `sextant list` shows the new agent

**Spec references**: [`specs/cli/commands.md`](../specs/cli/commands.md), [`specs/architecture.md`](../specs/architecture.md) §11b (Templates)

**Acceptance**: `sextant agents spawn assistant --template default` works end-to-end; agent appears in `sextant agents list`; first lifecycle frame on NATS.

---

### M12 — CLI completes bootstrap surface

**Goal**: every CLI verb needed to drive an agent works.

**Deliverables**:
- Verbs: `agents list|show|spawn|kill|restart|prompt`, `conversation [agent] [--tail]`, `pending`, `files read|ls|tail`, `exec`, `audit`, `traces show`
- `--json` flag on every verb for scripting
- Connection auto-discovery from `~/.config/sextant/client.toml`

**Spec references**: [`specs/cli/commands.md`](../specs/cli/commands.md)

**Acceptance**: full CLI walkthrough — spawn an agent, prompt it, watch its conversation, kill it, query audit log. All from the CLI.

---

### M13 — First TUI

**Goal**: one TUI built against the library to prove the framework.

**Deliverables**:
- `cmd/sextant-tui-agents/` — agent list TUI built on Bubble Tea + `sextant-client-go`
- Writes selected_agent to `ui.state.<operator>.selected_agent` (NATS KV)
- ~150 lines of code; demonstrates the "minimal TUI" pattern

**Spec references**: [`conventions/tui-conventions.md`](../conventions/tui-conventions.md)

**Acceptance**: `sextant-tui-agents` opens, lists agents, arrow-key nav updates selected_agent.

---

### M14 — Worktree management

**Goal**: agents can work in parallel via git worktrees.

**Deliverables**:
- MCP tools: `worktree_create`, `worktree_destroy`, `worktree_list`, `worktree_merge`, `worktree_diff`
- Worktree registry in NATS KV
- Merge serialization via `merge.lock` KV key
- Container spawn config wires worktree path as the `/workspace` mount

**Spec references**: `architecture.md` §11, [`conventions/git-workflow.md`](../conventions/git-workflow.md)

**Acceptance**: an agent can call `worktree_create`, run code there, commit, and merge back to main.

---

### M15 — Switchover readiness ✶ HEADLINE MILESTONE

**Goal**: initial is ready to take over development of itself.

**Deliverables**: not new code, but a verified state:
- All components healthy and supervised by sextantd
- Test suite passes against the running daemon
- Manual sanity check: assistant agent can be spawned and responds to a prompt
- Audit log is being written
- Worktree create → work → merge flow works end-to-end
- Operator can dispatch a real dev task to a sextant agent and it completes

**Acceptance**: the *first* sextant-driven dev task lands a PR (or commits to main directly). After this, classic CC retires from driving development.

---

### M16 — Self-update flow

**Goal**: sextant updates itself, with watchdog and rollback.

**Deliverables**:
- `self_update(target_revision)` RPC
- Build pipeline: agent's container does `go build` and `docker build`, stages to `/var/sextant/staging/`
- execv-style handoff for sextantd swap (~1s downtime; NATS + ClickHouse keep running)
- Watchdog process (separate from sextantd) verifies health for 60s, rollbacks on failure
- Test gate: `self_update` requires passing tests before staging
- Deploy lock via `deploy.lock` NATS KV key

**Spec references**: `architecture.md` §12

**Acceptance**: an agent can call `self_update`; if tests fail, no swap; if tests pass, sextantd swaps and the new version is running 60s later.

---

### M17 — Test environments

**Goal**: agents can run E2E tests against ephemeral isolated test environments.

**Deliverables**:
- MCP tools: `provision_test_environment`, `teardown_test_environment`, `list_test_environments`, `connect_to_test_environment`
- Namespaced isolation: separate NATS port, ClickHouse data dir, Docker container prefix, signing CA, config dir
- TTL-based reaper
- Test profiles: `default`, `minimal`, `multi_host`
- Resource quotas

**Spec references**: `architecture.md` §13

**Acceptance**: agent can provision a test env, run a full E2E test inside it (spawn test agents, trigger self_update, verify watchdog), tear it down. Production sextant unaffected throughout.

---

## After M17

Subsequent work is sextant-driven and tracked outside this bootstrap plan. The headline pillars that remain partially open in `specs/architecture.md` (UI hackability, viz specs, multi-user auth, multi-host federation specifics) get worked through by sextant agents as priorities dictate.

# Repository tour

A high-level map of the snapshot's tree.

## Top level

```
sextant/
├── cmd/                    # binary entry points
├── pkg/                    # libraries (one folder per logical unit)
├── clients/typescript/     # @sextant/client npm package
├── images/sidecar/         # Dockerfile + TS entrypoint for agent containers
├── specs/                  # design specs (architecture, components, protocols, CLI)
├── plans/                  # milestone plan, issues, incidents, reviews
├── conventions/            # Go style, git workflow, TUI conventions
├── skills/                 # SKILL.md files for agents
├── docs/book/              # this book
├── Makefile
├── go.mod / go.sum
└── README.md
```

## `cmd/` — binaries

| Path                          | What it builds                                                |
|-------------------------------|---------------------------------------------------------------|
| `cmd/sextant/`                | Operator CLI. See [CLI](../operator-guide/cli.md).            |
| `cmd/sextantd/`               | Supervisor daemon. See [sextantd](../components/sextantd.md). |
| `cmd/sextant-shipper/`        | NATS → ClickHouse shipper.                                    |
| `cmd/sextant-natsboot/`       | Standalone NATS bootstrap (test/dev harness).                 |
| `cmd/sextant-clickhouseboot/` | Standalone ClickHouse bootstrap.                              |
| `cmd/sextant-client-demo/`    | `pkg/client` Subscribe example.                               |
| `cmd/sextant-tui-agents/`     | M13 agent-list TUI.                                           |
| `cmd/sextantproto-gen/`       | JSON-schema generator for `pkg/sextantproto`.                 |

## `pkg/` — libraries

| Path                  | One-line summary                                                            |
|-----------------------|------------------------------------------------------------------------------|
| `pkg/sextantd/`       | Config types + RuntimeInfo for the daemon.                                  |
| `pkg/natsboot/`       | Start a `nats-server` subprocess; create streams + KV.                      |
| `pkg/clickhouseboot/` | Start a `clickhouse-server` subprocess; apply embedded migrations.          |
| `pkg/shipper/`        | NATS subscribers + ClickHouse writers + BoltDB spillover.                   |
| `pkg/shipperboot/`    | Supervise `sextant-shipper` from inside `sextantd`.                         |
| `pkg/supervisor/`     | Generic subprocess supervisor with backoff and quarantine.                  |
| `pkg/mcpserver/`      | In-process MCP server: 17 tools, HTTP + stdio transports, JWT auth.         |
| `pkg/containermgr/`   | Docker SDK wrapper: spawn, stop, exec, named volumes.                       |
| `pkg/templates/`      | Load agent templates from TOML files; sync to NATS KV.                      |
| `pkg/worktree/`       | Create, list, merge, destroy git worktrees; merge lock.                     |
| `pkg/authjwt/`        | Ed25519 signing CA; per-incarnation JWT issue and verify.                   |
| `pkg/rpc/`            | RPC dispatch table + handlers (one file per verb).                          |
| `pkg/client/`         | Go client library (used by `sextant`, TUIs, demos).                         |
| `pkg/sextantproto/`   | Source-of-truth Go types + generated JSON schemas.                          |
| `pkg/version/`        | `GitSHA` linker-injected at build time.                                     |

## `clients/typescript/`

The `@sextant/client` npm package. Mirrors the Go client surface for TS consumers (the sidecar, TS UIs). Types are generated from the Go JSON schemas via `npm run codegen` (`clients/typescript/scripts/codegen.ts`).

## `images/sidecar/`

The Debian-bookworm-slim container image for agents. Contents:

- `Dockerfile` — base image, tool install, sidecar staging.
- `test.sh` — M9 acceptance smoke test.
- `entrypoint/` — TypeScript code that runs inside the container; bridges NATS inbox ↔ Claude Agent SDK ↔ MCP server.

## `specs/`

The design layer. Authoritative for *intent*, sometimes ahead of code.

- `specs/architecture.md` — the design pillars (1–13). Decisions and rationale.
- `specs/components/` — per-component specs (sextantd, nats, clickhouse, shipper, sidecar-image, client-libraries).
- `specs/cli/commands.md` — CLI verb shapes.
- `specs/protocols/` — envelope schema, bus subjects, RPC catalog.

## `plans/`

The execution layer.

- `plans/bootstrap.md` — M0–M17 master plan.
- `plans/issues/` — open + closed bugs and feature requests (each a markdown file).
- `plans/incidents/` — postmortems.
- `plans/reviews/` — adversarial-review records.

## `conventions/`

- `STYLE.md` — Go style (Uber baseline + sextant additions).
- `git-workflow.md` — branch naming, merge lock, worktree rules.
- `tui-conventions.md` — keymap, status bar, `ui.state.*` patterns, theme tokens.

## `skills/`

`SKILL.md` files Claude Code (and sextant agents) load to understand how to operate inside the repo.

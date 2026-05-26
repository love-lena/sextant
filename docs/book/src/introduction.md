# Introduction

Sextant **initial** is a Go control plane for AI coding agents. It supervises a NATS JetStream bus, a ClickHouse store, and one Docker container per running agent. Each container runs a TypeScript sidecar that drives the Claude Agent SDK and reports back over the bus.

This book is a *reference* for the codebase as it currently exists at the snapshot's commit. It does not argue for the design — for that, read [`specs/architecture.md`](https://github.com/love-lena/sextant/blob/main/specs/architecture.md). It also does not document features that have been planned but not built; those land in [Milestone status](./reference/milestones.md) and [Known gaps and drift](./reference/known-gaps.md).

## What's in the repo

The bootstrap plan is split into 17 milestones (M0–M17). At the time of this snapshot, M0–M14 are merged on `main` and M15 (the switchover that flipped development from classic Claude Code to sextant-driven agents) has passed its smoke check. M16 (self-update) and M17 (test environments) are not implemented.

| Binary                  | Purpose                                                        |
|-------------------------|----------------------------------------------------------------|
| `sextantd`              | Daemon: supervises NATS + ClickHouse + shipper, hosts RPC + MCP |
| `sextant`               | Operator CLI                                                   |
| `sextant-shipper`       | NATS → ClickHouse pipeline                                     |
| `sextant-natsboot`      | Standalone NATS bootstrap (test harness)                       |
| `sextant-clickhouseboot`| Standalone ClickHouse bootstrap (test harness)                 |
| `sextant-client-demo`   | Subscription test harness for `pkg/client`                     |
| `sextant-tui-agents`    | The M13 first TUI: agent list browser                          |

Every binary above is built by `make install` (driven by the `CMDS` variable in `Makefile:23`). A separate code-generator at `cmd/sextantproto-gen/` exists for `go generate ./pkg/sextantproto/...` but is not part of the installed CLI set.

See [Repository tour](./getting-started/repo-tour.md) for the full layout.

## Reading order

- **If you're operating an install**: start with [Install](./getting-started/install.md) → [First run](./getting-started/first-run.md) → [CLI](./operator-guide/cli.md).
- **If you're working on a specific subsystem**: jump straight to the relevant chapter under [Components](./components/sextantd.md).
- **If you want the end-to-end picture**: read [Architecture overview](./architecture/overview.md), then [Data flow](./architecture/data-flow.md), then [Agent lifecycle](./architecture/lifecycle.md).
- **If you want to write a client or UI**: [Client libraries](./client-libraries/go-client.md) and the [Protocols](./protocols/envelope.md) section.

## How to trust this book

Every concrete claim is anchored to a file:line in the snapshot's tree. The snapshot lives on branch `docs/mdbook-snapshot` and is pinned to a commit on `main`. If a claim contradicts the code, the code wins — open an issue or fix the doc.

Forward-looking spec content (e.g. M16 self-update, M17 test environments, the §4a user-input propagation pattern) is called out as "not yet implemented" rather than presented as part of the current product. See [Known gaps and drift](./reference/known-gaps.md) for the full list of places where `specs/` and `pkg/` disagree.

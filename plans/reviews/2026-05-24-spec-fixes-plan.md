# Spec fixes plan — pre-phase-1 (2026-05-24)

This plan proposes concrete spec edits to resolve the blockers and top highs surfaced in [`2026-05-24-codex-adversarial.md`](2026-05-24-codex-adversarial.md). It is a **review-and-apply** document — the operator reads, approves (modifies, rejects, deltas), then the edits get committed. Phase 1 dispatch waits on this.

## Why this exists

Codex's adversarial review found 4 BLOCKERS and ~8 HIGHs that classic CC would hit in M0–M5. They are not sparse-spec ambiguities — they are contradictions between specs, ordering bugs, or load-bearing TBDs. A literal-minded CC implementor *should* stop and ask, and that defeats the "ship M0–M14 without blocking" goal.

These edits make the affected decisions authoritative in the specs so CC's per-milestone reads tell it exactly what to build.

## Conventions for this document

- **Severity** mirrors codex's tagging (BLOCKER / HIGH / MEDIUM).
- **Decision** is the proposed resolution — pick this one or counter-propose.
- **Edits** lists file:line ranges with before → after diffs (illustrative; final commit may format differently).
- **Why** is one paragraph of rationale.
- **Effort** is rough lines-of-spec changed.

---

## Apply order

1. §A — auth model split (unblocks M2 and M5, and removes principle violation)
2. §B — M2→M5 ordering + `sextant init` at M5 (unblocks M2 acceptance)
3. §C — Go workspace structure normative in M0 (unblocks M0)
4. §D — template format pinned (unblocks M11)
5. §E — CLI verb shape consistency (cosmetic but trips CC if not fixed)
6. §F — ClickHouse dedup semantics correct (unblocks M3/M6 correctness)
7. §G — Client subscribe seq metadata (unblocks M4 resume)
8. §H — NATS wildcard patterns correct (unblocks M2 streams)
9. §I — MCP transport pinned (unblocks M10)
10. §J — RPC semantics defined (unblocks M7)
11. §K — Shipper ack + drop policy (unblocks M6 correctness)
12. §L — Capability descoping principle reconciled with stub timeline (closes principle violation)
13. §M — Misc mediums (KV lock naming, ratatui leak, module path placeholder, trace ID optionality)

Items §A–§D are the **hard gate** — without them phase 1 cannot start cleanly. §E–§L are strong recommendations. §M is hygiene that pays for itself.

---

## §A — Auth model split: operator vs agent — BLOCKER

### Decision

`architecture.md` §10 is already internally consistent and authoritative:
- **Agents** authenticate to NATS with a per-incarnation JWT signed by the local CA (§10a).
- **Operator** authenticates by Unix file permissions on the socket (§10b). No operator JWT.

The component spec `nats.md` line 23 contradicts this by saying "every connection requires a JWT" without distinguishing agent vs operator. That's the bug. NATS supports per-listener auth policies (Unix-socket listener with no auth + TCP listener with JWT-required), which is the clean shape.

### Edits

**`specs/components/nats.md:18-23`** (config section)

Before:
```
- JetStream enabled with file-based storage at `~/.local/share/sextant/nats/`
- Listen address: configurable, default unix socket `~/.local/share/sextant/nats/nats.sock` for local connections + TCP localhost for tooling
- JWT auth using sextant CA (issued at `sextant init`)
- No anonymous access; every connection requires a JWT
```

After:
```
- JetStream enabled with file-based storage at `~/.local/share/sextant/nats/`
- Two listeners:
  - **Unix socket** at `~/.local/share/sextant/nats/nats.sock`. No NATS-level auth; access is gated by Unix file permissions. The operator (CLI, TUI, sextantd internals) connects via this socket. See `architecture.md` §10b.
  - **TCP localhost** (configurable port, default 4222). JWT auth required, signed by the sextant CA. Used by sidecar containers for agent connections. See `architecture.md` §10a.
- Anonymous access on the TCP listener is forbidden.
- CA is created by `sextant init` (M5); see §M2→M5 ordering note below.
```

**`specs/protocols/bus-subjects.md:90-97`** (subject ACLs section)

Before:
```
## Subject ACLs

Per-agent JWTs encode subject allowlists. Default agent caps:
- Publish: `agents.<own_uuid>.*`, `user_input.requests.<own_uuid>`, `user_input.responses.*`
- Subscribe: `agents.<own_uuid>.inbox`, anything declared by capability

Operator JWT: publish/subscribe `>`.
```

After:
```
## Subject ACLs

Per-agent JWTs encode subject allowlists. Default agent caps:
- Publish: `agents.<own_uuid>.*`, `user_input.requests.<own_uuid>`, `user_input.responses.*`
- Subscribe: `agents.<own_uuid>.inbox`, anything declared by capability

Operator: connects via the Unix socket listener; no JWT and no subject ACL — full publish/subscribe authority is implicit from socket access (see `architecture.md` §10b). NATS-level ACLs apply only to agents.
```

### Why

§10a/§10b in `architecture.md` already settled this; the component spec just hadn't caught up. Two-listener NATS is a standard pattern, not exotic. Resolving it here lets M2 ship the listeners as configured, M5 ship the CA, and M9–M11 ship the agent JWT issuance without re-litigating.

### Effort

~15 lines across 2 files.

---

## §B — M2→M5 ordering + `sextant init` available at M5 — BLOCKER

### Decision

Two related fixes:

1. **M2 ships the listeners without populating agent users.** The TCP listener is configured for JWT auth, but the user database is empty until M5's `sextant init` creates the CA and M11's first spawn issues the first user JWT. M2's acceptance criterion ("can roundtrip a message") is exercised on the Unix socket listener with the operator identity.
2. **The `cmd/sextant` CLI binary ships in M5 with the `init` subcommand** (and `doctor` for sanity checks), not in M11/M12. M11/M12 add the rest of the verbs. M5's acceptance criterion uses `sextant init`, so the binary must exist at M5.

### Edits

**`plans/bootstrap.md:70-86`** (M2 milestone)

Before (deliverables paragraph):
```
- Helper package (`pkg/natsboot/` or similar) that:
  - Generates a NATS config file from sextant config
  - Starts `nats-server -js` as a subprocess
  - Waits for ready, returns connection details
  - Creates all required JetStream streams (one per subject hierarchy) with appropriate retention
  - Creates all required NATS KV stores
- A standalone `cmd/sextant-natsboot/` for testing
- Test that exercises full bootstrap → connect → publish → consume → teardown
```

After (additions in bold; rest unchanged):
```
- Helper package (`pkg/natsboot/`) that:
  - Generates a NATS config file from sextant config with the two listeners specified in `specs/components/nats.md` (Unix socket: no auth; TCP localhost: JWT required, signed by the sextant CA). **In M2 the JWT user database is empty — agent-side connections are not yet exercised. The Unix socket listener carries all roundtrip tests.**
  - Starts `nats-server -js` as a subprocess
  - Waits for ready, returns connection details
  - Creates all required JetStream streams (one per subject hierarchy) with appropriate retention
  - Creates all required NATS KV stores
- **CA dependency**: M2 does not require the sextant CA. The TCP listener is configured to verify JWTs against a CA public-key file path, but the file is allowed to be empty/missing at M2 — only failed agent connections will fail open at this stage. M5 populates the CA.
- A standalone `cmd/sextant-natsboot/` for testing
- Test that exercises full bootstrap → connect → publish → consume → teardown **over the Unix socket listener**
```

Acceptance line stays the same.

**`plans/bootstrap.md:128-143`** (M5 milestone)

Before (deliverables):
```
- `cmd/sextantd/` with:
  - `sextant init` subcommand: generates CA keypair, writes config files, creates data dirs
  - Main daemon mode: starts NATS, starts ClickHouse, listens on a control socket
  - Component health monitoring + restart on failure
  - Signal handling: SIGTERM (graceful shutdown), SIGUSR2 (self-update execv handoff — stub for M16)
- Per-agent JWT issuance helpers (`pkg/authjwt/`)
- Operator capability discovery: just trust Unix file perms in initial
```

After:
```
- **`cmd/sextant/` binary** with at minimum:
  - `sextant init` subcommand: generates CA keypair at `~/.config/sextant/ca.{key,pub}`, writes `sextantd.toml` + `client.toml`, creates data dirs, creates default templates (see §D), idempotent re-runs.
  - `sextant doctor` subcommand: health diagnostics (NATS up, ClickHouse up, config valid, CA present). Used by M5's smoke test.
  - (Other verbs deferred to M12; the binary exists earlier to host `init`.)
- `cmd/sextantd/` with:
  - Main daemon mode: starts NATS, starts ClickHouse, listens on a control socket at `~/.local/share/sextant/sextantd.sock`
  - Component health monitoring + restart on failure
  - Signal handling: SIGTERM (graceful shutdown), SIGUSR2 (self-update execv handoff — stub for M16)
- Per-agent JWT issuance helpers (`pkg/authjwt/`). Issuance flow plumbed but no agent consumes it until M11.
- Operator authority: Unix file perms on `~/.config/sextant/` and `~/.local/share/sextant/nats/nats.sock`. No operator JWT.
```

Acceptance line becomes: `sextant init && sextantd` starts cleanly with NATS + ClickHouse running and healthy; `sextant doctor` reports green.

**`specs/components/sextantd.md`** (binary roles section, near top)

Add a short note clarifying that `sextant` (operator CLI) and `sextantd` (daemon) are two binaries, both in this repo: `cmd/sextant/` and `cmd/sextantd/`. `init` lives in `sextant`, not `sextantd`.

### Why

Two ordering bugs in one decision. CA depends on init; init depends on the `sextant` binary; the `sextant` binary was deferred to M11/M12. Fixing it once unblocks M2 (no CA dependency) and M5 (init exists), and lets M3/M4/M6 use `sextant doctor` for their own smoke tests.

### Effort

~30 lines across `bootstrap.md` and `sextantd.md`.

---

## §C — Go workspace structure normative in M0 — BLOCKER

### Decision

Pick `cmd/<binary>/` + `pkg/<module>/` (standard Go layout) and lock module path. Drop "TBD".

### Edits

**`plans/bootstrap.md:32-46`** (M0 milestone)

Before (deliverables):
```
- `go.mod` at repo root
- Go workspace structure (TBD: monorepo with `cmd/`, `pkg/`, etc. — see `specs/components/sextantd.md`)
- `.golangci.yml` with strict config (...)
- `Makefile` or `taskfile.yml` with `lint`, `test`, `build`, `fmt`
...
```

After:
```
- `go.mod` at repo root, module path `github.com/lhh/sextant-initial` (FIXME: confirm org name with operator before applying — placeholder follows the same TBD as in `STYLE.md`)
- Repo layout (normative):
  - `cmd/sextant/` — operator CLI binary (full set of verbs added across M5/M11/M12)
  - `cmd/sextantd/` — daemon binary
  - `cmd/sextant-shipper/` — shipper binary (M6)
  - `cmd/sextant-sidecar/` — sidecar entrypoint binary (M9, but Go-side parts may land earlier)
  - `cmd/sextant-tui-agents/` — first TUI (M13)
  - `pkg/sextantproto/` — shared envelope/event types (M1)
  - `pkg/natsboot/` — NATS bootstrap (M2)
  - `pkg/clickhouseboot/` — ClickHouse bootstrap (M3)
  - `pkg/client/` — Go client lib (M4)
  - `pkg/authjwt/` — JWT issuance helpers (M5)
  - `pkg/shipper/` — shipper logic (M6)
  - `pkg/mcpserver/` — MCP server (M10)
  - `pkg/worktree/` — worktree management (M14)
  - Additional `pkg/` subpackages as milestones require, each placed under the relevant binary's logical scope.
- `.golangci.yml` with strict config (`govet`, `staticcheck`, `errcheck`, `gosec`, `revive`, `gocritic`, `nilaway`, `gofumpt`)
- `Makefile` with `lint`, `test`, `build`, `fmt` targets (and `sidecar-image` later for M9). Prefer plain `make` over `taskfile.yml`.
- CI config (GitHub Actions, `.github/workflows/ci.yml`) running `make lint test` on every push.
- `.editorconfig`, `.gitignore` for Go projects.
```

**`conventions/STYLE.md:95-98`** — replace placeholder `github.com/.../sextant-initial` with chosen module path (or leave as `{{module}}` with a footnote).

### Why

Every later spec assumes concrete `cmd/`, `pkg/` paths. Lock now. Module path is a single decision — pick before M0 starts.

**Operator decision needed**: confirm module path. Options:
- `github.com/lhh/sextant-initial`
- `github.com/lena/sextant-initial`
- `github.com/lhickson/sextant-initial`
- something else

### Effort

~25 lines in `bootstrap.md` + a 1-line edit in `STYLE.md`.

---

## §D — Template format pinned for M11 — BLOCKER

### Decision

Templates are TOML files stored at `~/.config/sextant/templates/<name>.toml`, loaded into the NATS `templates` KV bucket on `sextant init`. Schema is intentionally minimal for initial; expandable later.

### Edits

**`specs/architecture.md` (near §9 or §11)** — add a short subsection `Templates`:

```
### Templates

An agent template is a TOML file that defines the spawning shape of an
agent class. Stored at `~/.config/sextant/templates/<name>.toml`,
loaded into NATS KV bucket `templates` on `sextant init`.

Schema (initial — extend cautiously):

    name = "default"                  # template name (also the file stem)
    description = "..."
    image = "sextant-sidecar:latest"  # container image
    permissions = ["read_file", "list_dir", "send_message"]  # cap allowlist
    env = { KEY = "value" }           # env vars injected into the container
    mounts = ["worktree"]             # named mount classes
    initial_prompt = ""               # optional first prompt on spawn
    model = "claude-opus-4-7[1m]"     # model id passed to Claude SDK
    permission_ceiling = "auto"       # max permission mode (locked to "auto")

Default template `default.toml` ships with `sextant init` and serves
as the minimal spawnable agent. Operators add templates by dropping
TOML files in the templates directory and re-running `sextant init`
(idempotent) or via a future `sextant templates reload` verb.
```

**`specs/cli/commands.md:36-38`** (sextant init bullet list) — already mentions templates; confirm the line `Creates initial templates at ~/.config/sextant/templates/` and add the format ref:
```
- Creates initial templates at `~/.config/sextant/templates/`; format defined in `specs/architecture.md` (Templates section).
```

**`plans/bootstrap.md:233-244`** (M11) — no edit needed, but add a one-line reference: "Templates: see `specs/architecture.md` Templates section."

### Why

`sextant spawn assistant --template default` cannot ship without a template format. The codex-flagged problem is real but small — one schema, one default file. Pin both.

### Effort

~20 lines across `architecture.md` and `cli/commands.md`.

---

## §E — CLI verb shape consistency — HIGH (cosmetic but trip-hazard)

### Decision

The plan uses `sextant spawn` and `sextant list`, but `cli/commands.md` establishes `sextant <noun> <verb>`. CC will read both, see the contradiction, and pick one — possibly the wrong one. Pin to `sextant agents spawn` and `sextant agents list` everywhere.

### Edits

**`plans/bootstrap.md:244`** (M11 acceptance):

Before:
```
**Acceptance**: `sextant spawn assistant --template default` works end-to-end; agent appears in `sextant list`; first lifecycle frame on NATS.
```

After:
```
**Acceptance**: `sextant agents spawn assistant --template default` works end-to-end; agent appears in `sextant agents list`; first lifecycle frame on NATS.
```

**`plans/bootstrap.md:248-259`** (M12) — already lists verbs correctly using `sextant agents list|show|spawn|...`; no edit needed.

**`README.md`** — grep for stale `sextant spawn` / `sextant list` usages and update.

### Effort

~5 lines.

---

## §F — ClickHouse dedup semantics — HIGH

### Decision

Switch the `events`, `audit`, and per-OTel-class tables from `MergeTree` to `ReplacingMergeTree` keyed on `(id, ts)` (or `(id)` alone for tables where ts is part of the natural ID). Document that "dedup" requires `FINAL` or background merges; queries that need exact dedup should use `argMax`-style or `OPTIMIZE … FINAL` semantics.

### Edits

**`specs/components/clickhouse.md:34-50`** (events table) — engine line and a note:

Before:
```
) ENGINE = MergeTree()
ORDER BY (subject, ts, id);
```

After:
```
) ENGINE = ReplacingMergeTree(ts)
ORDER BY (subject, ts, id);

-- Dedup: ReplacingMergeTree keeps the row with the highest `ts` per
-- ORDER BY key. Queries that require strict dedup should use FINAL
-- (slow) or argMax aggregation. For shipper re-deliveries, identical
-- (id, ts) pairs collapse on background merge.
```

**`specs/components/clickhouse.md:54-66`** (audit table) — same engine swap, same comment.

**`specs/components/shipper.md:5,27-32`** — change "dedup on ClickHouse primary key" to:

Before (line 5):
```
Subscribe to NATS subjects, write to ClickHouse. At-least-once delivery with dedup on ClickHouse primary key.
```

After:
```
Subscribe to NATS subjects, write to ClickHouse. At-least-once delivery; ClickHouse tables use `ReplacingMergeTree` so re-deliveries of the same `id` collapse on background merge or via `FINAL` queries. See `specs/components/clickhouse.md`.
```

Lines 27-32 (delivery semantics):

Before:
```
ClickHouse dedup via primary key on `id` (UUID per event). Re-inserts are no-ops.
```

After:
```
ClickHouse dedup via `ReplacingMergeTree` engine on `(id, ts)`. Re-inserts with the same `id` are collapsed on background merge or via `FINAL` on read. Not instant — pipelines that read fresh data should either tolerate transient duplicates or use `FINAL` (with the perf cost). For initial scale this is acceptable.
```

### Why

`MergeTree` ORDER BY is not a uniqueness constraint. Codex was right; the original spec misread how ClickHouse dedup works. `ReplacingMergeTree` is the standard fix and well-understood — boring code, no exotic patterns.

### Effort

~15 lines across 2 files.

---

## §G — Client subscribe seq metadata — HIGH

### Decision

Subscribe returns a typed `Message` wrapper carrying envelope + stream metadata, not raw `Envelope`. Sequence numbers, subject, ack handle are all accessible. Mirrors NATS' own `jetstream.Msg` shape.

### Edits

**`specs/components/client-libraries.md:11-19`** (Go API):

Before:
```go
// Subscribe to a subject pattern.
func (c *Client) Subscribe(ctx context.Context, subject string, opts ...SubscribeOption) (<-chan Envelope, error)

// SubscribeFromSeq does gap-fill replay from a sequence then transitions to live.
func (c *Client) SubscribeFromSeq(ctx context.Context, subject string, fromSeq uint64) (<-chan Envelope, error)
```

After:
```go
// Message wraps a received envelope with stream metadata.
type Message struct {
    Envelope    Envelope
    Subject     string
    StreamSeq   uint64    // JetStream stream sequence
    ConsumerSeq uint64    // JetStream consumer sequence
    Timestamp   time.Time // JetStream-reported receive ts
    ack         func() error
}

func (m *Message) Ack() error  { return m.ack() }

// Subscribe to a subject pattern.
func (c *Client) Subscribe(ctx context.Context, subject string, opts ...SubscribeOption) (<-chan Message, error)

// SubscribeFromSeq does gap-fill replay from a stream sequence then transitions to live.
func (c *Client) SubscribeFromSeq(ctx context.Context, subject string, fromSeq uint64) (<-chan Message, error)
```

**`specs/components/client-libraries.md:62-76`** (TS API) — mirror the change: `AsyncIterable<Message>` with the same fields.

**`specs/components/client-libraries.md:84`** (shared concerns):

Before:
```
**Reconnection**: built-in with exponential backoff; loss of connection emits an event on a special control channel; client subscriptions auto-resume from the last seen seq
```

After:
```
**Reconnection**: built-in with exponential backoff; loss of connection emits an event on a special control channel; client subscriptions auto-resume from `Message.StreamSeq` of the last-acked message.
```

### Why

Resume-from-seq is a load-bearing primitive (used by every TUI's scrollback and every audit query). Without seq exposed on the API, it's impossible to implement. Wrapping in `Message` is standard.

### Effort

~25 lines in `client-libraries.md`.

---

## §H — NATS wildcard patterns correct — HIGH

### Decision

Multi-token subjects need `>`, not `*`. Audit `nats.md` and `bus-subjects.md` for every wildcard.

### Edits

**`specs/components/nats.md:29-39`** (streams table):

Before (relevant rows):
```
| `telemetry_traces` | `telemetry.traces.*` | 7 days | TBD |
| `telemetry_metrics` | `telemetry.metrics.*` | 30 days | TBD |
| `telemetry_logs` | `telemetry.logs.*` | 7 days | TBD |
| `user_input` | `user_input.*` | 30 days | TBD |
| `control_rpc` | `sextant.rpc.*` | 1 day | TBD |
```

After:
```
| `telemetry_traces` | `telemetry.traces.>` | 7 days | TBD |
| `telemetry_metrics` | `telemetry.metrics.>` | 30 days | TBD |
| `telemetry_logs` | `telemetry.logs.>` | 7 days | TBD |
| `user_input` | `user_input.>` | 30 days | TBD |
| `control_rpc` | `sextant.rpc.>` | 1 day | TBD |
```

Same for `agent_frames`, `agent_lifecycle`, `audit` — confirm whether subjects are exactly one extra token (use `*`) or multi-token (use `>`). Looking at the subject map:
- `agents.<uuid>.frames` is 3 tokens — `agents.*.frames` is correct as written.
- `agents.<uuid>.lifecycle` is 3 tokens — `agents.*.lifecycle` is correct.
- `audit.*` only matches one extra token, but audit subjects are 2-token (`audit.spawn`, `audit.kill`, etc.), so `audit.*` is also correct.

Net: only `telemetry.*`, `user_input.*`, and `sextant.rpc.*` need the `>` upgrade.

**`specs/protocols/bus-subjects.md:79-89`** — wildcard subscription patterns are already mixed; confirm `telemetry.traces.>` is correct (it is) and that any spec mentions match.

### Why

`subject.*` matches exactly one token. `subject.>` matches one or more. The streams as configured would silently drop traffic from multi-token producers (e.g. shipper publishing `telemetry.metrics.shipper.lag_seconds`).

### Effort

~5 lines.

---

## §I — MCP transport pinned — HIGH

### Decision

MCP server is in-process inside sextantd, exposed via **stdio over Unix socket** to local MCP clients and via **HTTP/SSE on a localhost-bound port** for the sidecar to reach across the container boundary. Architecture §9c says "in-process" is the lean; just pin the transport question.

For initial: sidecar connects to the MCP server via HTTP/SSE on `http://host.docker.internal:<port>/mcp` (OrbStack equivalent of Docker Desktop's host networking). Transport is **Streamable HTTP** per the MCP spec — the standard remote transport in MCP 2025+.

### Edits

**`specs/architecture.md`** §9c (or new subsection right after §9c):

Add:
```
**MCP transport — DECIDED**:
- MCP server runs in-process inside sextantd.
- Local clients (CLI, TUI) connect via Unix socket using MCP's stdio framing.
- Sidecars connect via Streamable HTTP at `http://<host>:<port>/mcp`. Host port configured by `sextantd.toml`; sidecar reaches it via `host.docker.internal` (OrbStack/Docker Desktop) or `host.containers.internal` (Podman).
- Identity propagation (§9c "Open"): the sidecar presents its agent JWT in the `Authorization: Bearer <token>` header on every MCP request. MCP server verifies JWT + capability per tool call.
- Initial does not implement MCP-over-NATS. If that becomes useful later (e.g. cross-host), it is additive — current transports remain.
```

**`specs/components/sidecar-image.md:45-48`** (sidecar entrypoint section) — add a note about how the sidecar reaches the MCP server: env var `SEXTANT_MCP_URL=http://host.docker.internal:<port>/mcp` injected at spawn.

### Why

Without a transport, M10 can't be implemented. Streamable HTTP is the boring MCP-standard answer for remote transport. Stdio for local clients matches Claude Code's pattern.

### Effort

~15 lines across 2 files.

---

## §J — RPC request/response/streaming semantics — HIGH

### Decision

Define an RPC contract at `specs/protocols/rpc-catalog.md`:
- Every RPC request is an envelope on `sextant.rpc.<verb>` with `reply_to` set to a caller-provisioned ephemeral subject.
- Every response is one or more envelopes on the reply subject. The final envelope's payload includes `"_terminal": true`.
- Streaming RPCs publish multiple responses on the same reply subject; cancellation = caller unsubscribes (server detects via NATS' subject-no-subscribers signal and stops emitting).
- Errors are returned as envelopes with `payload.error` populated and `"_terminal": true`.
- Audit: every RPC entry logs an `audit.rpc` envelope before dispatch; every response logs `audit.rpc_result`.

### Edits

Add a new section at the bottom of **`specs/protocols/rpc-catalog.md`** titled **Wire semantics**, containing the bullet list above plus a code sketch of the Go server-side dispatcher.

### Why

M7 implements RPC server-side, M12 uses it for `query_history` etc. Without contract semantics, CC will invent something divergent between server and client.

### Effort

~30 lines (new section).

---

## §K — Shipper ack + drop policy — HIGH

### Decision

- Shipper acks JetStream **only after** durable write succeeds. Failed writes leave the message pending; JetStream re-delivers per its consumer config.
- The local BoltDB buffer is **for ClickHouse-down windows only**, not a primary durability layer. It absorbs the gap so JetStream's pending queue doesn't grow indefinitely. Drained as soon as ClickHouse is back.
- When buffer hits cap (10GB default): **fail closed** — stop pulling from JetStream, surface as a critical metric + audit event. No silent oldest-drop. Operator intervention restores flow.

### Edits

**`specs/components/shipper.md:34-40`** (backpressure):

Before:
```
When ClickHouse is unreachable or slow:
- Local buffer at `~/.local/share/sextant/shipper-buffer/` using BoltDB or similar embedded KV
- Buffer drains as ClickHouse recovers
- Buffer-depth metric published periodically
- Hard cap on buffer size (10GB default); over cap → oldest events dropped with an audit event
```

After:
```
When ClickHouse is unreachable or slow:
- Local buffer at `~/.local/share/sextant/shipper-buffer/` using BoltDB. Acts as a finite spillover for ClickHouse-down windows; JetStream remains the durable source of truth.
- Shipper acks JetStream **only after** the message has been durably written to ClickHouse (or persisted to the BoltDB buffer with a pending-write entry).
- Buffer drains as ClickHouse recovers; drained entries are then acked on JetStream.
- Buffer-depth metric published periodically.
- Hard cap on buffer size (10GB default). On hitting the cap: **fail closed** — shipper stops pulling from JetStream and emits a critical `audit.shipper_backpressure` event. JetStream's own retention (per-stream max-bytes) becomes the limiting factor. Operator intervention required to drain or extend buffer.
- No silent oldest-event drop. If the operator wants degraded mode (drop-oldest), it must be explicitly enabled via `shipper.degraded_mode = "drop_oldest"` in config — off by default.
```

### Why

Two bugs: ack ordering was unspecified, and "drop oldest" contradicted "no event loss". Fail-closed by default is the correct posture; degraded mode is opt-in.

### Effort

~15 lines.

---

## §L — Capability descoping reconciled with stub timeline — HIGH

### Decision

Two options; recommend **option 2**.

1. Ship real JWT verification at M10 (when the MCP server first lands and agents could call tools). Update bootstrap.md so M10 acceptance includes "MCP tool call rejected when JWT lacks the cap."
2. Rename the principle from "capability descoping" to "capability descoping with stubbed enforcement in phase 1" in the docs, with a hard ship-date for real enforcement (M16-adjacent, before sextant agents drive serious work).

Recommend **(1)**. JWT issuance ships in M5; verifying it in M10 is a small lift relative to the trust story.

### Edits

**`plans/bootstrap.md:212-225`** (M10):

Add to deliverables:
```
- **Real JWT verification on every tool call**. Sidecar presents JWT in `Authorization: Bearer` header (see §I MCP transport); MCP server validates signature against the CA from M5, extracts cap list, rejects calls outside the allowlist with a structured error. This is what makes capability descoping non-theater (architecture.md §10a).
```

Update M10 acceptance:
```
**Acceptance**: a sidecar can call `send_message` and the message lands on the destination agent's inbox subject. **A sidecar with a JWT missing the `send_message` capability is rejected with a clear error.**
```

**`plans/bootstrap.md:171-172` (M7), `:219-220` (M10), `:237-238` (M11)** — replace "stubbed until JWTs land" / "allow-everything-for-now" with "JWTs verified per §10a; allowlist enforcement applies from M10 onward".

### Why

§10a explicitly says "without this, capability descoping is just a convention" and "this is needed even with one human operator." Shipping the stubbed timeline as specced betrays that principle. The lift to verify in M10 is small and load-bearing for the autonomy story.

### Effort

~10 lines.

---

## §M — Misc mediums

### M.1 KV lock naming

Resolve `merge_lock`/`merge.lock` inconsistency between `nats.md` and `architecture.md` + `git-workflow.md`. Recommend bucket `locks`, keys `merge` and `deploy`. Edit `nats.md:51-52` accordingly.

### M.2 ratatui leak

`architecture.md:361` mentions ratatui as an option. Remove or explicitly allow Rust TUIs alongside Bubble Tea / Ink. Recommend removing; sextant initial is Go + TS.

### M.3 Module path placeholder

Tied to §C. Pick one module path and replace `github.com/.../sextant-initial` everywhere.

### M.4 Trace ID optionality

`envelope-schema.md:18-21` makes `trace_id`/`span_id` optional but `architecture.md:289` says every envelope carries them. Decide: require on every envelope (and document root-event-self-trace behavior, i.e. envelope is its own root and `trace_id` defaults to `id`), or document the optional cases. Recommend require + self-root behavior — keeps trace assembly simple.

### M.5 Sandbox config

`architecture.md:90,155-159` leaves sandbox/networking open. Pin a minimal initial: container mounts `/workspace` (worktree), `/sextant-config` (read-only); env vars for `SEXTANT_NATS_URL` + `SEXTANT_MCP_URL` + `SEXTANT_JWT`; network bridge with egress allowed (codex flagged this as principle-violation; for initial we accept and document).

### M.6 OTel schema

`clickhouseboot/migrations/` for OTel tables needs concrete columns. Adopt OpenTelemetry's ClickHouse exporter schema (well-known reference). 1–2 hours of spec work to copy from that exporter's repo into our `clickhouse.md`.

---

## Operator decisions needed before applying

1. **Go module path** (§C). Pick: `github.com/lhh/sextant-initial`, `github.com/lena/sextant-initial`, or other.
2. **§L choice**: real JWT verification at M10 (recommend) vs principle rename.
3. **§M.5 sandbox network egress**: accept open egress for initial + flag for v2 hardening (recommend), or scope narrower from day one.
4. Any of the §M items the operator wants to defer rather than fix now.

Everything else is recommend-and-apply; counter-propose any specific edit you want changed.

---

## What this plan deliberately does NOT do

- **Self-update + test envs before switchover** (codex bonus bet-against #3): leaving as-is. Codex is right that the autonomy story is weak without M16/M17 before M15, but reordering is a larger rework. Mitigation: the goal file stops CC at M14 + M15 smoke checks; the first sextant-driven task is operator-dispatched, with operator-controlled deploy. M16/M17 become the first sextant-driven tasks themselves, which is actually a clean bootstrap test.
- **"Full firehose, filter later" redesign** (codex bonus bet-against #2): leaving as-is. Real concern but a v2-class redesign. Recommend filing an issue and adding a `Retention/redaction policy` open question to `architecture.md` §8 to track it.
- **Everything-on-NATS-and-ClickHouse risk** (codex bonus bet-against #1): leaving as-is. This is the bet sextant initial is built on; the right response is to fail-fast on the first sign it doesn't hold (which we will see during the first sextant-driven dev loop).

---

## Estimated total effort

If all §A–§L are applied: ~3–4 hours of spec edits + review. §M is another ~1–2 hours.

After: dispatch phase 1 with the goal we drafted, and CC should ship M0–M14 without operator input.

# Sextant initial design pillars

Naming: **pilot** = the current experimental version (v0). **initial** = the planned properly-designed version (v1). Future versions: v2, v3, etc. Throughout this doc, "initial" refers to what we're designing here.

## Decided foundation

- **Go** as the implementation language. Native NATS/Docker/ClickHouse/OTel ecosystems, fast build times for the AI-agent dev loop, simpler concurrency model for sextant's I/O-heavy workload. Style: Uber Go style guide + `golangci-lint` (strict, including `nilaway`) + "boring code" enforced via CI.
- **NATS JetStream** as universal event bus. The bus is the IPC for everything that crosses a component boundary.
- **ClickHouse** as the long-term queryable store, from day one. Single binary, sextantd supervises it as a subprocess.
- **Claude Code SDK (TypeScript)** as the single runtime, per-agent sidecar processes. No pi support. SDK sidecar is TS regardless of sextant's language choice.
- **Container per agent incarnation** as the real isolation boundary (OrbStack on macOS).
- **OpenTelemetry data model** as the standard for traces/spans/metrics/logs — no runtime OTel SDK dep required (small lib dep acceptable if convenient).
- **Git worktrees** as the parallel-iteration pattern. Each agent gets its own worktree as its workspace mount; multiple agents work on independent branches concurrently.

## Bootstrap strategy

Initial is built in two phases:

1. **Classic-CC phase (pre-switchover)**: detailed plan + spec files in the repo. Classic Claude Code (CLI mode) reads from these and implements them. Linear, well-scoped, no self-iteration needed. Builds the bootstrap-minimum: libraries + CLI + sidecar image + first TUI(s).
2. **Sextant-driven phase (post-switchover)**: once initial is functional enough to spawn its first agent, the switchover happens. From that point, sextant agents iterate on sextant itself in parallel via worktrees. Self-iteration / self-update flow becomes critical (see §11).

The switchover is the headline milestone. Before it: classic CC drives. After it: sextant drives.

## Repo strategy

**New empty repo for initial development** (e.g., `sextant-initial`). No pilot Rust code visible to agents — prevents pattern-copying from pilot we don't want.

Initial repo contents on day one (no code, just specs):

```
sextant-initial/
├── README.md                 # vision + pointer to plans
├── plans/                    # bootstrap.md + per-milestone plans
├── specs/                    # detailed component + protocol specs
│   ├── components/           # nats, clickhouse, sextantd, shipper, sidecar-image, libraries
│   ├── cli/                  # CLI verb shapes
│   └── protocols/            # bus subjects, rpc catalog, envelope schema
├── conventions/              # STYLE.md, tui-conventions, git-workflow
└── skills/                   # agent skills (SKILL.md files)
```

Eventual cutover (when initial is mature): force-push initial's tree to the existing `sextant` repo main, tag pilot's tip as `pilot-final`, archive `sextant-initial`. One-time operation. Names settle to `sextant` = initial codebase going forward.

## Three-layer data architecture

| Layer | Purpose | Examples |
|---|---|---|
| **NATS streams** | Event log, working window (~7 days) for gap-fill + live replay | Agent frames, lifecycle, audit, telemetry, RPC results |
| **NATS KV** | Current state (latest values, not history) | Agent definitions (current version), viz specs, operator preferences |
| **ClickHouse** | Long-term queryable store, indefinite retention | All events forever (or per policy), telemetry, audit, definition version chains |

A `sextant-shipper` component subscribes to NATS subjects and writes to ClickHouse with at-least-once delivery (ClickHouse dedupes on PK).

Roughly in order of impact: if any of 1–5 are wrong, expect a v2 (the version after initial).

## Follow-on questions from the SDK decision

These are the immediate downstream sub-questions that need answers before the initial design is concrete:

- **A. SDK language**: TypeScript or Python? (TS likely — Node startup is fast, smaller deps, TS is claude code's native ecosystem so the SDK is most polished there.)
- **B. Sidecar ↔ sextant transport**: sidecar joins NATS directly and sextant just supervises the process, vs. a local control channel between sextant and sidecar with frames going on the bus. (Bus-only is cleaner and matches the "everything on bus" lean from #6.)
- **C. Session storage**: where does `session_id` live? Agent definition record (durable, in NATS KV or ClickHouse)? Cached in the sidecar's working dir? Both?
- **D. Tool layer**: sextant-specific tools (like "send message to agent X" or "publish to bus subject") — implemented as MCP server the sidecar connects to, or as custom tools defined per-agent?
- **E. Sidecar bundling**: ship the SDK sidecar code as part of the sextant binary (extract on first run), or as a separate npm/pip package? Affects install story.

## 1. Runtime adapter model — DECIDED

**Decision**: Claude Code SDK as the runtime, run as a per-agent sidecar process. Sextant Go core owns orchestration, supervision, identity, NATS routing, audit, TUI. Each agent's SDK sidecar owns the agent loop and tool execution — full claude code tool ecosystem (Bash, Read, Edit, Task, MCP, skills, hooks, slash commands, plan mode). Sidecars talk to sextant *only via NATS* — never via stdin/stdout pipes. **Skip pi runtime support entirely** — one runtime path, claude SDK only.

What this unlocks for the other pillars:
- **#2 (identity)**: agent definition stores a persistent SDK session_id; process restart spawns a new sidecar with `--resume <session_id>`, conversation resumes natively. "Resume" stops being a hack.
- **#3 (sandbox)**: the sidecar process is the sandbox boundary. Filesystem scope, network egress, env vars set at sidecar spawn time.
- **#4 (inter-agent)**: sidecar exposes a "send to agent X" tool (via MCP or custom) that publishes to `agents.X.inbox` on NATS. Native to the agent loop, not bolted on.
- **#5 (supervisor)**: sextant supervises well-behaved Node processes, not raw CLI subprocesses parsing JSON-lines from a pipe.

Open sub-decisions this triggers (see follow-on questions below).

## 2. Agent identity & lifecycle — DECIDED (Model B, definition-centric)

**Model**: agents are persistent records ("definitions"). Processes are *incarnations* — they come and go independently of the agent's existence. An agent can be defined-but-not-running indefinitely.

**Storage**: NATS KV (built into JetStream).

**Agent definition fields**:
- UUID (permanent, primary identity)
- Name (human-readable, unique among non-archived agents)
- Type (template reference)
- Runtime config (model, provider, params)
- Tool allowlist (derived from capability set, see §9c)
- Sandbox config (TBD — see §3)
- Current session_id
- Lifecycle state: defined / running / paused / archived
- Version (monotonic counter)

**Identity rules**:
- UUID is primary and permanent. All bus messages reference UUIDs internally.
- Name is unique among living agents; reusable after archival. **No `_n` suffixing**.
- Names are mutable; UUIDs are not.

**Versioning**:
- Every definition mutation publishes a change event on the bus (audit-trail via ClickHouse).
- Monotonic version counter per agent, **not semver**.
- Three update modes:
  - **Hot-reload** — apply to live incarnation without restart.
  - **Restart** — apply at next incarnation spawn.
  - **Fork** — create a *new* agent (new UUID) from this one.

**Templates** (separate first-class concept):
- A template is a recipe for creating agents.
- Fan-out a template → N distinct agents, each with own UUID.
- Templates are versioned independently from the agents spawned from them.

**Incarnation rule**: at most one running incarnation per agent UUID.

**Host pinning**: agents pinned to a host. Cross-host migration is out of scope for initial.

**Archived state**: definition + past session transcripts preserved, never re-run, name released for reuse.

**Open sub-questions** (track, don't resolve now):
- **Hot-reload field matrix**: which fields are safe to hot-reload (tool allowlist, runtime params?), which require restart (system prompt / charter — session-baked), which require fork.
- **Template fan-out mechanics**: name pattern (`dev-{n}`)? auto UUIDs? inherit + override semantics?
- **Fork semantics**: does fork inherit parent's session_id (branch the conversation) or start fresh? Probably configurable, default fresh.
- **Name release timing**: when does an archived agent's name become available — immediately, after a grace period, manual unlock?

## 3. Sandbox / isolation model — DECIDED (real isolation via containers)

**Model**: each agent's SDK sidecar runs in its own container. Container is the technical boundary; *policy inside is permissive by default* (broad mounts, open internet, full tool access). Anthropic's published patterns for unattended Claude Code all use containers, so this aligns with the SDK ecosystem.

**Container runtime**: OrbStack on macOS (free, fast, native-feeling); Docker-compatible elsewhere. Sextant requires a container runtime — no bare-process fallback.

**Container lifecycle**: container per agent incarnation. Container is spawned with the incarnation, dies with it. Persistent state lives in named volumes, not in the container's writable layer.

**Internet access**: **open by default**. Containers have unrestricted network egress unless an agent definition declares restrictions. Internet access is part of "permissive sandbox" — the container provides the boundary; broad capability is the default.

**Base image**: `sextant-sidecar:<version>` — Debian-based, rich tool set baked in:
- Node + Claude Code SDK + sextant's sidecar runtime + MCP client
- Common CLIs: git, gh, jq, ripgrep, fzf, curl, build tools
- Language toolchains: Go, Node + npm, python + pip (for general-purpose use). Rust intentionally omitted since sextant is Go.
- Image size is a one-time pull cost; per-spawn latency matters every spawn.

**Persistent volumes** (per-agent, mounted at well-known paths):
- `~/.claude` — agent's claude state (per-agent, real isolation)
- `~/.cargo`, `~/.npm`, `~/.local/share`, `~/.cache` — tool/package-manager state persists across restarts (`cargo install` results survive)
- `~/.config/gh`, `~/.gitconfig` — per-agent (or declare host-mount if shared auth wanted)

**Credentials & secrets**: sextant injects per-agent credentials at container spawn time, via env vars or mounted secret files. Agent definition includes a `credentials` block listing which secrets from the host's secret store (`~/.config/sextant/secrets/` or equivalent) to inject.

**Workspace mounts**: declared per-agent. Typically `/Users/lena/dev/<repo>` rw. The host's `~/.ssh` is *not* mounted by default; declare it per-agent if needed for git push.

**Resource limits**: declared per-agent (CPU, memory). Cheap insurance against runaway agents.

**Sidecar bundling (follow-on Q E from §1)**: bundled into the base image. No extraction-on-first-run.

**Sandbox config — DECIDED minimum for initial**:

Container mounts at spawn (resolved from the template's `mounts` field — see §11b):

| Mount class | Container path | Source | Mode |
|---|---|---|---|
| `worktree` | `/workspace` | agent's git worktree on host | rw |
| `secrets` | `/run/sextant/secrets/` | per-template subset of `~/.config/sextant/secrets/` | ro |
| (built-in) | `/home/agent/.claude` | per-agent named volume | rw |
| (built-in) | `/home/agent/.{cargo,npm,cache,local/share}` | per-agent named volumes | rw |
| (built-in) | `/home/agent/.gitconfig`, `~/.config/gh` | per-agent or host-bind (per template) | per-template |

Container env vars (always set):
- `SEXTANT_AGENT_UUID`, `SEXTANT_AGENT_NAME`, `SEXTANT_HOST_ID`
- `SEXTANT_NATS_URL` — TCP listener URL the sidecar reaches via `host.docker.internal` (NATS Server has no Unix-socket transport; see `specs/components/nats.md` §"Config")
- `SEXTANT_JWT` — per-incarnation token signed by the M5 CA
- `SEXTANT_MCP_URL` — Streamable HTTP endpoint (typically `http://host.docker.internal:5172/mcp`)
- `SEXTANT_SESSION_ID` (optional) — present if the SDK should `--resume`

Networking:
- macOS host: use **OrbStack with host-networking enabled** for containers. The container reaches the host's NATS TCP listener at `host.docker.internal:4222` and the MCP server at `host.docker.internal:5172`.
- Egress: **open by default** for initial. Per-agent egress restrictions are a future feature; codex flagged this as a security risk and we accept the tradeoff for the single-operator phase. Document as a v2 hardening pillar.

**Open sub-decisions**:
- **Custom per-agent images**: do we support agent definitions that declare extra system packages, building a derived image at definition time? Lean: yes for initial, but ship without it and add when first needed.
- **Image build & distribution**: ship the image build script (operator builds locally), or pull from a registry (and which registry — ghcr.io? docker hub?). Lean: ship the build script for initial; registry distribution later.
- ~~**`.claude` volume seeding**: when a per-agent `.claude` volume is created, what's it seeded with? Empty (agent starts fresh)? Snapshot from host's `~/.claude`? Per-template defaults? Lean: empty by default; template can declare `.claude` seeding source.~~ **Resolved**: templates carry an optional `claude_seed` host path (§11b). When set, the spawn handler — controlled by `claude_seed_mode` — either copies the host dir into a per-agent named volume (default `copy-on-spawn`, lets the SDK persist its session journal) or bind-mounts it read-only (legacy opt-in `readonly-bind`). Unset = empty per-agent volume (default). See `slug:feat-template-claude-seeding` and `slug:bug-claude-seed-readonly-breaks-session-persistence`.

## 4. Inter-agent communication

Today agents only talk via Lena prompting them. With the bus, "agent A publishes to `agents.lead.inbox`, lead subscribes" is natural. But the design question is: do we *want* it? Specifically — should lead autonomously prompt dev pods? Is there a routing/permission layer (A can talk to B but not C)? This is initial-or-never; retrofitting later means redesigning every agent's worldview.

Partial answers already locked: §9c MCP tools include `send_message`, `prompt_agent`, `broadcast`, `subscribe_to_subject`. Permission scoping (§9c) controls *which* agents get which tools. So "agents can talk" is decided; routing/permission specifics are policy questions on top.

### 4a. User input propagation (Claude SDK pattern)

Sextant should have a **first-class pattern for propagating user-input requests** built on the Claude Code Agent SDK's user-input mechanism. The feature shape:

- **Layered review**: a request can flow through intermediate reviewers (e.g. dev pod → lead → Lena) before reaching the human. Each reviewer can answer, escalate up, or batch with related requests.
- **Grouped / batched requests**: the human-facing UI aggregates pending requests across agents so Lena isn't whiplashed by N separate dialogs.
- **Bus-native**: requests are events on a dedicated subject hierarchy (`user_input.requests.<from_uuid>`, `user_input.responses.<request_id>`). Answers are also events. Audit trail comes for free.
- **Agent definitions specify routing**: each agent has an `escalate_to` field (another agent's UUID, or `user`). Default `escalate_to` might be `user` (direct), but supervising agents like `lead` declare downstream pods escalate to *them*.
- **Tools**: `request_user_input(question, options, urgency, group_with?)` on the agent side; `answer_request`, `defer_to_user`, `batch_requests` on the reviewer side.

Not a critical decision — it's a feature design that should be done well, but doesn't gate the initial architecture. Open sub-questions for later:
- Schema for grouped/batched requests
- Timeout/expiration policy for unanswered requests
- How agents *wait* for responses (ties to blocking-tool-call question in §9c)
- Whether reviewers can amend questions or only answer/escalate/defer
- UI affordances for the batched-requests inbox

## 5. Supervisor model — DECIDED (reasonable defaults)

Per-agent supervision policy lives in the agent definition. Reasonable defaults; tune per agent as needed.

- **Restart policy**: restart-on-failure with exponential backoff (e.g. 1s, 2s, 4s, ... capped at 5min). Max retries before quarantining the agent (default ~5) so a perma-broken agent doesn't flap forever.
- **Resource limits**: declared per agent (CPU shares, memory cap). Enforced via container limits (the container is already the boundary from §3).
- **Health checks**: three signals — (1) container alive, (2) sidecar process alive inside container, (3) periodic heartbeat from sidecar onto the bus (`agents.<uuid>.heartbeat` every N seconds). All three must hold; missing heartbeats → restart.
- **Graceful shutdown**: SIGTERM with grace period (default 10s, configurable), then SIGKILL. The SDK sidecar handles SIGTERM by flushing in-flight tool calls and persisting session_id before exiting.
- **State derivation**: supervision state derives from container state + bus heartbeats — *no separate registry* to drift out of sync. This kills v1's state-desync class of bugs structurally.
- **Per-agent overrides**: any of the above can be overridden in the agent definition (e.g. a long-running batch agent might want restart-on-failure: never).

## 6. Control plane — DECIDED (everything-on-bus, NATS request/reply for RPC)

Two surfaces, both on NATS:

- **Bus subscriptions**: streams of events (agent frames, lifecycle, telemetry, audit). Read-only consumption.
- **RPC via NATS request/reply**: "do this now" or "fetch that now" operations. Single substrate (NATS), two patterns (subject subscription vs request/reply).

This means "everything on the bus" — events AND RPCs both flow over NATS subjects. No separate gRPC/HTTP layer.

### RPC catalog (UI/operator-facing)

**Container/filesystem access** (sextantd executes against the agent's container):
- `read_file(agent_id, path)` → bytes + content_type
- `list_dir(agent_id, path)` → entries
- `stat(agent_id, path)` → metadata
- `read_file_stream(agent_id, path, follow=true)` → streaming reply (`tail -f` over RPC)
- `exec_in_container(agent_id, cmd, args[])` → stdout/stderr/exit_code (operator-capability-gated, audited)

**Agent state queries**:
- `get_agent_status(agent_id)` → definition + incarnation status + recent activity counts
- `get_session_summary(agent_id)` → session_id, message count, last activity
- `query_history(agent_id, filter, time_range)` → parametrized ClickHouse query, returns past events

**On-demand telemetry**:
- `trigger_thought_dump(agent_id)` → sidecar publishes current internal SDK state to a debug subject
- `enable_verbose_logging(agent_id, duration)` → sidecar boosts publish verbosity for a window

**Lifecycle / control**:
- `spawn_agent`, `kill_agent`, `restart_agent`, `prompt_agent` (also exposed as §9c MCP tools for agent-initiated control)

### Authorization

Every RPC has a required capability. UI-as-operator gets the operator capability set via §10b (Unix file perms). Agents calling RPC carry their §10a JWTs. RPC layer reuses the agent-auth substrate — no new authz model.

### Idempotency

Control RPCs (`spawn_agent`, etc.) carry a client-generated idempotency key. Sextantd dedupes on the key for a bounded window (~60s) to prevent accidental double-execution from retries.

### Sidecar default verbosity

Sidecar publishes **everything observable about the SDK** to the bus by default — internal SDK events, API calls, reasoning steps, tool decisions, full firehose. Filtering happens at query time (ClickHouse), not at publish time.

### Open sub-decisions

- **`exec_in_container` scope**: arbitrary command exec (powerful, operator-capability) vs fixed safe-verb set. Lean arbitrary; the operator already has full container access on the host — this just routes it through the bus for audit.
- **RPC subject naming**: flat `sextant.rpc.<verb>` vs per-agent-scoped `agents.<uuid>.rpc.<verb>`. Lean flat for initial; per-agent allows finer NATS subject ACLs in v2.
- **Streaming RPC patterns**: multi-message reply vs temporary subject the caller subscribes to. NATS supports both — TBD which fits sextant's client SDK ergonomics better.

## 7. Multi-host federation — DECIDED (hub-and-spoke topology)

**Topology**: hub-and-spoke. The architecture has four logically separate components; deployment chooses where they live:

- **NATS hub** — cluster anchor; worker NATS leaf nodes connect to it
- **ClickHouse instance** — anywhere reachable from all sextantds
- **Signing CA** — key file kept secure wherever the operator chooses (could be USB, encrypted vault, dedicated host)
- **Operator host** — wherever the human sits and types; can roam between sessions

**Default initial deployment**: all four components on one machine (the operator's laptop). Multi-host is just a deployment migration — peel components off to dedicated hosts as needed without code changes.

### Component placement decisions

1. **Worker hosts**: each runs sextantd + containers + local `sextant-shipper`. NATS leaf node connects to hub. Shipper writes to ClickHouse over network.
2. **Cross-host auth**: shared CA signing key; each host has a worker cert issued by CA for NATS account membership. Per-agent JWTs signed by the same CA, transparent across hosts.
3. **Agent host-pinning**: agent definitions include `host_pin: <host_id>` (§2). Spawn RPCs route to the pinned host's sextantd.
4. **Discovery**: manual `~/.config/sextant/cluster.toml` listing known hosts. v2: mDNS or gossip.
5. **Time sync**: NTP required on all hosts for consistent timestamps in ClickHouse.
6. **Shipper buffering**: workers buffer locally (small embedded queue) when ClickHouse is unreachable; replay on reconnect.

### Failure handling

- **Worker offline**: NATS cluster detects disconnection; agents on that worker go silent. UI shows host offline. On rejoin, sextantd reconciles agent state.
- **Hub offline**: leaf nodes can't reach each other; agents keep running locally but cross-host pub/sub stops. Recovery on hub return.
- **ClickHouse offline**: shipper buffers; live events flow through NATS unaffected. UI history queries fail until ClickHouse returns.
- **CA key unavailable**: existing JWTs continue working until expiry; new agent spawns can't issue tokens until CA is reachable.

### Open sub-decisions

- **HA for any single component**: v2 concern. initial accepts that each component is a SPOF in its scope.
- **Federated ClickHouse**: only relevant if a single instance becomes a bottleneck; v2.
- **Network transport**: TLS-secured NATS over public internet vs only private networks (Tailscale, WireGuard, SSH tunnels). Support both; default to TLS+worker-certs.
- **CA key custody**: lives on the operator's laptop by default; could be USB stick, KMS, etc. Operator's choice.
- **Cross-cluster trust** (multiple sextant deployments talking to each other): pure v2.

## 8. Observability — DECIDED (OTel data model, single backend, no new services)

**Format**: OpenTelemetry data model for traces, spans, metrics, and logs. `sextant-proto` defines our own types matching the OTel schema — no runtime dep on the OTel SDK required (small library dep acceptable if convenient, but not mandatory).

**Storage**: everything goes through the same NATS → ClickHouse pipeline as other events. No Prometheus, no Jaeger, no separate observability services to operate.

**Subject layout**:
- `telemetry.traces.<host>` — span events
- `telemetry.metrics.<host>` — metric measurements
- `telemetry.logs.<host>` — log records (for framework/dep noise that isn't a domain event)

**Trace propagation**: every bus envelope carries `trace_id` + `span_id` so any event can be cross-referenced with its trace. This is the key design property — makes a heterogeneous query story navigable instead of siloed.

**Forward compatibility**: if we ever outgrow ClickHouse queries and want a dedicated trace viewer (Jaeger, Tempo, Honeycomb), a one-shot exporter reads from ClickHouse or telemetry subjects and pushes OTLP. No upstream code changes needed — our wire format is already standard.

### What goes where

| Signal | On the bus? | Where retained |
|---|---|---|
| Agent frames, lifecycle, control | Yes | NATS streams + ClickHouse |
| Audit events | Yes | NATS streams + ClickHouse (long-term) |
| OTel traces/spans | Yes | ClickHouse |
| OTel metrics | Yes | ClickHouse |
| Framework/dep logs | Yes (`telemetry.logs.*`) | ClickHouse |
| Sidecar internal SDK events | Yes (firehose default) | NATS streams + ClickHouse |
| Heartbeats | Yes (§5) | NATS streams (rolling) |

### Open sub-decisions

- **OTel library dep**: bring `opentelemetry-go` as a convenience (the reference implementation; mature, well-tested) or stay fully dep-free and roll our own emitters. Lean: use `opentelemetry-go` since it's first-class in Go and matches our ecosystem-native preference.
- **Sampling**: full traces from chatty agents could be high volume. Head-based sampling at the SDK sidecar? Tail-based at the shipper? Probably no sampling for initial — let ClickHouse handle the volume.
- **Retention policy**: keep everything forever (small enough at sextant's scale), or buckets by event kind (audit forever, frames 90 days, telemetry 30 days)?
- **Log shipping**: framework/dep logs as bus subjects vs structured stderr files. Lean bus subjects for consistency — one query path.

## 9. Plugin / extension model

Three plugin categories, each needs its own design.

### 9a. UI plugins — DECIDED (framework + many small UIs)

**The framework is the product, not the TUIs.** Sextant ships a great client library; UIs are demonstrations and convenient defaults. Building a new TUI/CLI/web-UI/Slack-bot is a trivial exercise against the library.

**Client libraries** (both first-class, built in parallel):
- `sextant-client-go` (Go) — used by the `sextant` CLI and any Go TUI
- `@sextant/client` (TypeScript) — used by the SDK sidecar (every agent process) and any TS UI
- Same primitives, different language. Languages are picked per-consumer based on fit.

**Library API surface**:
- NATS connection (auto-loads `~/.config/sextant/client.toml`)
- Auth (Unix-perm in initial; JWT-ready for v2 multi-user)
- Bus subscription helpers — subject patterns, gap-fill, typed deserialization
- RPC helpers — request/reply with timeouts, idempotency keys, typed responses
- Shared types (generated from `sextant-proto` via JSON Schema)
- `ui.state.*` NATS KV helpers for inter-UI coordination
- Reconnection / retry policies built in

**Multi-UI coordination via the bus**: shared `ui.state.*` NATS KV namespace lets independent UIs coordinate (selected agent, focused pane, filters). No central UI orchestrator; UIs auto-sync when run together, work standalone when run alone.

**Decoupled from any multiplexer**: sextant ships TUIs and a CLI. How you arrange them (zellij, tmux, separate terminal windows) is the operator's choice. No hard dependency on zellij.

**Bootstrap shipping list**:
1. `sextant-client-go` (Go) + `@sextant/client` (TS) — the libraries
2. Extended `sextant` CLI — covers list/show/spawn/kill/restart/prompt/conversation/pending/files/exec/audit/traces
3. A couple of TUIs built on the library to prove the pattern (agents list, conversation viewer, pending queue)
4. **`SKILL.md` for agents to author new TUIs/CLIs** — replaces a `sextant new` subcommand; uses Claude Code's skill mechanism
5. Style guide (`ai-docs/tui-conventions.md`): keymap conventions, status bar layout, `ui.state.*` patterns, theme tokens

**Live vs history vs hybrid** (UI data access patterns):
- **Live**: `client.subscribe(subject)` → NATS streaming
- **History**: `client.query(filter, time_range)` → ClickHouse via `query_history` RPC
- **Hybrid** (v1 pattern, carry forward): `client.subscribe(subject, from_seq=N)` → JetStream replays then transitions to live

**Protocol versioning**: additive-only event schemas; `proto_version` field in every envelope. Breaking changes get a new subject namespace and dual-publish during transition.

**Type definitions**: Go (`sextant-proto`) is source of truth. Generate JSON Schema from Go structs (e.g. `invopop/jsonschema` or similar) at build time. TS clients use `json-schema-to-typescript`. **Keep JSON on the wire** — debuggability beats binary-wire compactness at sextant's scale.

**Discovery / connection**: `~/.config/sextant/client.toml` (NATS connection string, signing key path). CLI flag overrides for one-off connections.

**UI auth in initial**: relies on §10b — UI runs as local Unix user, gets operator caps via filesystem perms.

### Open sub-decisions

- **CLI output formats**: human-readable default + `--json` for scripting on every command? Lean yes.
- **Shared rendering library scope** (`sextant-tui` / `@sextant/tui`): how opinionated? Lean small — just enough to make common patterns easy (agent list component, conversation view component, status bar). Exotic UIs use Bubble Tea (Go) or Ink (TS) directly per `conventions/tui-conventions.md`.
- **TUI skill scope**: one big skill (`sextant-ui-author.md`) or several focused skills (TUI / CLI / event-consumer / RPC-caller)? Lean: a few focused skills, easier for agents to load just what they need.
- **Agent-extensible visualizations**: design pattern is "viz specs in NATS KV, agents publish specs, UIs interpret per their rendering capabilities." Defer detailed spec vocabulary to initial-late or v2 — the bus+RPC model already gives UIs full data access.
- **Plugin sandboxing**: do third-party UI plugins run with full operator caps? In initial single-operator land, yes (trust). v2 multi-user adds operator-JWT-with-restricted-caps for untrusted plugins.
- **Subject visibility**: lean all subjects readable by operator-trusted UIs in initial; finer ACLs in v2.
- **Build priority**: Go client first (CLI depends on it, CLI is bootstrap-critical); TS client in parallel (SDK sidecar depends on it, also bootstrap-critical). Both must ship together.

### 9b. Runtime adapter plugins — COLLAPSED (Claude SDK only for initial)

§1 locked sextant initial to the Claude Code SDK as the sole runtime. Multi-provider support (OpenAI, Gemini, local models via Ollama/vLLM) is a v2 concern. No adapter abstraction needed in initial — there's one path.

### 9c. Tool plugins — MCP server

**Decided**: sextant ships an MCP server that the SDK sidecar connects to. This is the surface that makes sextant a platform rather than just an orchestrator. Four categories of tools, all mediated by sextant:

- **Communication**: `send_message(agent, content)`, `broadcast(subject, content)`, `subscribe_to_subject(...)`, `wait_for_agent(...)`
- **Introspection**: `list_agents()`, `agent_status(name)`, `query_audit(filter)`, `query_history(agent, time_range)` (pull another agent's recent context)
- **Control**: `spawn_agent(...)`, `kill_agent(...)`, `restart_agent(...)`, `prompt_agent(...)`
- **System**: `emit_event(...)`, `read_config(...)`, `get_metric(...)`

**Permission model — decided shape**:
- Sextant controls per-agent tool access; agents do not get the full catalog by default.
- **Capability descoping**: a spawned agent's permission set is a *subset* of the spawner's. Agents cannot grant themselves or their children capabilities they don't have.
- No hardcoded special roles — "lead" is just a configuration today; initial will have many agent types as Lena experiments. Capability sets are declarative per agent type, not baked into the codebase.

**MCP transport — DECIDED**:
- MCP server runs **in-process** inside sextantd.
- **Local clients** (CLI, TUI) connect via Unix socket using MCP's stdio framing.
- **Sidecars** (inside containers) connect via **Streamable HTTP** at `http://<host>:<port>/mcp`. The host port is configured by `sextantd.toml` (default 5172). Sidecars reach it via `host.docker.internal` on OrbStack/Docker Desktop or `host.containers.internal` on Podman. The exact host is injected via the `SEXTANT_MCP_URL` env var at spawn time.
- **Identity propagation**: sidecars present their per-incarnation JWT (M5+) in the `Authorization: Bearer <token>` header on every MCP request. MCP server verifies signature against the sextant CA, extracts capability list, and authorizes per tool call.
- Initial does not implement MCP-over-NATS. If cross-host MCP becomes useful later, it is additive — current transports remain.

**Open sub-questions** (track here, don't resolve now):
- **Blocking vs async semantics**: which tools are fire-and-forget (`send_message`), which are query-and-return (`list_agents`), which block (`prompt_agent_and_wait_for_response`, `wait_for_agent_to_finish`). Deadlock policy — timeouts only, or active deadlock detection?
- **Tool versioning**: do tools get versioned (`sextant_send_message_v1`)? What happens to running agents when a tool signature changes?
- **Exact tool catalog**: the four categories above are scope; the specific tool list per category needs filling in during initial implementation.
- **Capability descoping mechanism**: how is "subset of spawner's caps" enforced — explicit list at spawn time, or implicit "inherit minus declared drops"? Lean: explicit list at spawn time (operator/spawner declares the subset; sextantd validates).

## 10. Auth & authz — DECIDED (split into 10a + 10b)

Auth has two independent slices that were originally lumped together. Agent auth is required regardless of human count; human auth is the actual single-user-vs-multi-user question.

### 10a. Agent auth — DECIDED (real substrate from day one)

The mechanism that makes §9c's capability descoping enforceable. Without this, capability descoping is just a convention — agent A could technically call any tool because the MCP server can't verify who's calling.

- Every agent incarnation gets an identity token at spawn time encoding: agent UUID, capability set (tool allowlist + subject allowlist), incarnation lifetime
- MCP server verifies the token on every tool call; rejects if missing caps
- NATS verifies subject access on every pub/sub
- Token is **immutable per incarnation** — no in-flight cap expansion; revocation = kill the incarnation
- Implementation: per-agent JWT signed by sextant's local signing key. JWT system is also the substrate for cross-host federation (§7).
- **Capability descoping enforcement**: spawner's JWT must carry caps it wants to grant. Sextant verifies subset relationship at spawn time and signs the child's JWT accordingly.

This is needed even with one human operator. It's how §9c stops being theater.

### 10b. Human operator auth — DECIDED (trust local Unix user for initial)

Single-operator on a personal dev machine. Local Unix file permissions are a real security boundary. No operator JWTs, no logins, no token rotation — just file perms on the socket and the secrets directory.

- Operator credential = Unix user owns `~/.config/sextant/` (specifically `operator.creds` for NATS — see `specs/components/nats.md`) and the sextantd control socket (`~/.local/share/sextant/sextantd.sock`). NATS itself has no Unix-socket transport, so the NATS-side boundary is the `0600`-perm creds file.
- TUI/CLI: any process running as the Unix user has full operator authority
- Audit trail still records actor (`lena` hardcoded today) for forensic completeness and forward-compat
- Multi-user is a v2-or-when-needed concern — adding operator-JWT layer later does not require redesigning anything else
- **Permission ceiling**: `--permission-mode auto` is the max for any agent (locked policy). Higher ceilings never granted.

**Open sub-questions** (track, defer):
- Multi-user activation criteria — what triggers the v2 multi-user push (first teammate? open-sourcing? specific feature ask?)
- Cross-host signing chain for §7 — shared root key vs per-host peer trust
- Secret rotation policy — manual today, automation later
- Operator action audit — even with file-perm-only auth, every operator action (`sextant agents kill`, `sextant agents spawn`, etc.) is recorded with `actor=lena` on the bus

## 11. Parallel iteration via git worktrees — DECIDED

Each agent gets its own git worktree as its workspace mount; agents work on independent branches concurrently.

```
~/dev/sextant/                       # main worktree (operator's working dir)
~/dev/sextant-worktrees/
  ├── feat-bus-routing-001/          # dev agent A's worktree
  ├── feat-tui-conversation-002/     # dev agent B's worktree
  └── fix-clickhouse-migration-003/  # dev agent C's worktree
```

**Lifecycle**:
- Created when a task is assigned (lead agent calls `worktree_create(name, base_branch)` MCP tool)
- Mounted into the owning agent's container as `/workspace`
- One agent per worktree (enforced at spawn time)
- After review pass: `worktree_merge(name)` merges branch into main
- Cleaned up post-merge or per idle policy

**Worktree registry**: `worktrees.<name>` NATS KV entries with status, owning_agent, base_branch, created_at, last_activity. Queryable, watchable.

**Cache sharing**: shared volumes for module/build caches (`~/go/pkg/mod`, `~/.cache/go-build`) — per-worktree-class, not per-agent.

**Merge serialization**: single merge into main at a time via NATS KV lock `locks.merge` (bucket `locks`, key `merge`, TTL 5 min). Conflicts surface as user-input requests (§4a).

**Merge strategy (M14 — DECIDED)**:

The merge must land on the target branch (typically `main`) without touching the operator's main working tree (the operator may have uncommitted state, may be on a different branch, etc.). M14 implements this with a **dedicated transient merge worktree**:

1. Acquire `locks.merge` (bucket `locks`, key `merge`, TTL 5 min).
2. Create a transient worktree at `<WorktreesRoot>/.merge-<target>-<short-rand>/` checked out on `<target>` (if a stale `.merge-*` worktree exists from a crashed prior merge, the daemon removes it first via `git worktree remove --force`).
3. Run `git -C <merge_worktree> merge --no-ff <branch>` inside it.
4. On conflict: abort the merge (`git merge --abort`), tear down the transient worktree, release the lock, return `MergeResult{Conflicts: [...], Branch, Target}`. The source branch is unchanged; the operator can resolve and retry.
5. On clean merge: the target ref is now advanced. Tear down the transient worktree (`git worktree remove`). Update the source worktree's KV entry to `status=merged`. Release the lock.
6. Push to remote is **out of scope for M14** — the merge is local only. A future milestone wires remote push (and re-pulls the source branch into the transient worktree before merging) once a remote is configured.

This approach has three properties:

- **No mutation of operator's main worktree.** The operator can be on any branch, with any working-tree state, throughout the merge.
- **No long-lived merge worktree.** The transient one is created and torn down per merge; no state to garbage-collect.
- **Crash-safe.** A crash mid-merge leaves the transient worktree on disk; the next merge under the lock removes it and starts fresh.

Limitations accepted for M14:

- **No remote push.** Local merges only.
- **No concurrent merges to different targets.** The lock serializes all merges regardless of target; a future spec change may scope the lock by target if needed.
- **No CI gate.** The merge proceeds unconditionally on a clean merge result; M14 acceptance only requires the bytes land. Test-gated merges are a separate concern (M16-era).

**Worktree naming**:

- **Operator/agent-created worktrees**: `<kind>-<short-description>-<seq>` per `conventions/git-workflow.md` (`kind` ∈ `feat | fix | refactor | docs | test | chore | spec`).
- **Agent-spawn worktrees** (the worktree created automatically when a template's `mounts` includes `worktree`): `feat-<template_name>-<short_uuid>-001`, where `<short_uuid>` is the first 8 chars of the agent's UUID. The `feat-` prefix is fixed so the name validates against the `<kind>-<desc>-<seq>` rule applied uniformly by `worktree.ValidateName` — agent-spawn worktrees are stored in the same KV with the same naming gate as operator-driven ones, just with a deterministic kind. Stable per agent; the agent can later create its own task-shaped worktree via the MCP tool and switch its work to it.

**MCP tools** (§9c control category):
- `worktree_create(name, base_branch)` → path
- `worktree_destroy(name)` (operator-cap)
- `worktree_list()` → all worktrees with status
- `worktree_merge(name, target=main)` → merges or returns conflict report
- `worktree_diff(name, against=main)` → diff output

**Open sub-decisions**:
- **Branch naming convention**: `<kind>-<short-desc>-<seq>`? Auto-generated from task title?
- **CI per worktree**: agents run tests in their container before declaring done; results in audit log
- **Pruning policy**: idle > 14 days → archive; > 30 days → delete? Configurable
- **Multi-host worktrees**: agents in the same task stay on the same host (worktrees are host-local filesystems)

## 11b. Templates — DECIDED

An agent template is the spawning recipe for an agent class. Templates are the source for `sextant agents spawn <name> --template <T>` (M11).

**Storage**: TOML files at `~/.config/sextant/templates/<name>.toml`. On `sextant init` (M5), the file contents are loaded into NATS KV bucket `templates` (keyed by template name). Operators add or change templates by dropping/editing files there; a future `sextant templates reload` verb (post-initial) re-syncs into KV. For initial, re-running `sextant init` is the reload path (idempotent).

**Schema** (initial — extend cautiously, additive only):

```toml
name = "default"                       # template name (also the file stem)
description = "..."
image = "sextant-sidecar:latest"       # container image
permissions = [                        # cap allowlist (subset of operator's caps)
  "read.agents", "read.history",
  "control.prompt", "control.worktree",
]
env = { KEY = "value" }                # env vars injected into the container
mounts = ["worktree"]                  # named mount classes (worktree | secrets | ...)
initial_prompt = ""                    # optional persistent charter; passed to the SDK as systemPrompt on every turn
model = "claude-opus-4-7[1m]"          # model id passed to Claude SDK
permission_ceiling = "auto"            # max permission mode (locked to "auto")
claude_seed = "~/.config/sextant/assistant-claude"  # optional; host dir surfaced at /home/agent/.claude
claude_seed_mode = "copy-on-spawn"      # optional; "copy-on-spawn" (default) | "readonly-bind"
```

**`claude_seed`** (optional): when set, sextantd resolves the path (`~/` expands via `os.UserHomeDir`) and surfaces the directory into the container at `/home/agent/.claude`, replacing the default empty per-agent volume. Use it to pre-load operator-curated CLAUDE.md, slash commands, hooks, or `settings.json` for the agent class. Missing or non-directory paths fail template validation at load time. Two-way sync (operator edits → agent, agent writes → host) is intentionally out of scope; the agent's writes stay container-local. See `slug:feat-template-claude-seeding`.

**`claude_seed_mode`** (optional; only meaningful when `claude_seed` is set):

- `"copy-on-spawn"` (default): on first spawn for an agent UUID, sextantd creates a Docker named volume `sextant-claude-seed-<uuid>`, populates it by copying the host seed dir contents, and mounts the volume **rw** at `/home/agent/.claude`. Subsequent spawns of the same agent (e.g. `restart_agent --preserve-session`) reattach the existing volume — the populate step is skipped — so the Claude Agent SDK's session journal under `projects/<encoded-cwd>/<session-id>.jsonl` survives across incarnations. When an agent is archived the volume is removed. This is the right behavior for assistant-style agents that need multi-turn conversation continuity.
- `"readonly-bind"`: legacy opt-in. Bind-mount the host seed dir read-only at `/home/agent/.claude`. Suitable for one-shot agents that genuinely don't need the SDK to write anything; **multi-turn session resume does not work** in this mode (the SDK can't write its journal to a RO mount, so `SEXTANT_SESSION_ID` resume on the next turn fails). Operators who pick this mode have explicitly opted into that trade-off.

See `slug:bug-claude-seed-readonly-breaks-session-persistence`.

**`initial_prompt`** (optional): persistent context, **not** a one-shot first user message. sextantd base64-encodes the field, injects it as `SEXTANT_INITIAL_PROMPT`, and the sidecar passes the decoded string to the Claude Agent SDK as `systemPrompt` — so the model sees it on every turn for the lifetime of the incarnation (and the next spawn of the same agent, since the field is part of the persisted `AgentDefinition`). Use it for charter / role / preferences that should steer every reply; for one-shot greetings or seeding, just send a regular prompt. See `slug:bug-initial-prompt-not-forwarded-to-sdk`.

**Default template**: `default.toml` ships with `sextant init`. Contents:

```toml
name = "default"
description = "Minimal spawnable agent — assistant-style, broad reads, restricted writes."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "read.history", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
```

**Mount classes**: declared names that sextantd resolves to actual container mounts at spawn time. Initial classes:
- `worktree` → the agent's git worktree → `/workspace`
- `secrets` → the per-template subset of `~/.config/sextant/secrets/` → read-only mount
- `ssh` → the host's `~/.ssh` directory (resolved via `os.UserHomeDir`) → `/home/agent/.ssh` **read-only**. Opt-in only; default templates do **not** include it. Use when the agent class is trusted to use the operator's SSH identity for `git push` to GitHub. See `slug:feat-container-ssh-passthrough`. Unknown values in `mounts` fail template validation at load time so a typo like `"shh"` surfaces immediately rather than silently producing an agent missing the intended mount.

**Open sub-decisions** (defer):
- Template versioning — do we track template hashes per incarnation for forensics? Lean yes via `agent_definitions_history` table.
- Operator-only fields — should any template field be operator-edit-only? Lean: whole file is operator-only since it sits in `~/.config/sextant/`.
- Cross-template inheritance — `extends = "default"`? Not in initial; copy fields into each file.

## 12. Bootstrap & self-improvement — DECIDED (two phases)

### Phase 1: Classic-CC bootstrap (pre-switchover)

Initial is built first by classic Claude Code reading detailed plan + spec files in the new repo. No self-iteration needed during this phase — it's linear, well-scoped work.

Deliverables of phase 1:
- All foundation components functional (NATS, ClickHouse, sextantd, shipper, sidecar image)
- `sextant-client-go` + `@sextant/client` libraries
- Extended `sextant` CLI with all bootstrap-essential verbs
- At least one TUI built on the library (proves the pattern)
- Skill files for agents to extend the system

### The switchover (headline milestone)

The deliberate moment when initial takes over from classic CC. Readiness checklist:
- All components healthy and supervised
- Test suite passes against the running daemon
- Manual sanity check: first sextant agent (assistant) can be spawned and respond
- Audit log is being written
- Worktree create/merge flow works end-to-end
- Operator can dispatch a real dev task and watch it complete

### Phase 2: Sextant-driven self-improvement (post-switchover)

Once the switchover happens, sextant agents iterate on sextant itself in parallel via worktrees. Self-update flow becomes critical.

**Self-update flow (`sextant self up` analog)**:
1. Agent/operator triggers `self_update(target_revision)` RPC
2. Sextantd checks out target revision in a staging worktree, runs the build:
   - `go build` for sextantd + CLI + libraries
   - `docker build` for sidecar image
3. Stage to `/var/sextant/staging/<revision>/`
4. Atomic swap of sextantd via execv-style handoff (sextantd is the only thing that swaps; NATS + ClickHouse keep running)
5. Watchdog process (separate from sextantd, since sextantd is being replaced) verifies for 60s:
   - New sextantd alive
   - NATS still reachable
   - ClickHouse still reachable
   - At least N agents heartbeating
   - No exception spike in audit log
6. Healthy → deploy event audited as success; old binary archived
7. Unhealthy → execv rollback to previous binary; failure audited

**What swaps vs what stays**:

| Component | Frequency of restart | Mechanism |
|---|---|---|
| **sextantd** | Frequent (every code change) | execv-style handoff, ~1s downtime |
| **shipper** | Frequent | restart with sextantd, local buffer covers gap |
| **NATS** | Rare (only NATS version bumps) | full restart, brief downtime, operator-initiated |
| **ClickHouse** | Rare (version bumps) | full restart, data dir persists, migrations on startup |
| **Sidecar base image** | Per sidecar code change | rebuild; new agents use new image; existing opt-in via `--resume` |
| **Per-agent sidecar** | Per agent's choice | restart with `--resume <session_id>` preserves conversation |

**Version compatibility during deploys**:
- Bus envelopes: additive-only schemas, `proto_version` field; new sextantd reads old events
- RPCs: every RPC declares supported `proto_version` range; out-of-range → structured error
- Sidecars and sextantd within N minor versions interop

**Concurrent deploy protection**: NATS KV `locks.deploy` (bucket `locks`, key `deploy`, TTL 10 min) prevents two agents attempting `self_update` simultaneously.

**Test gate**: `self_update` requires passing tests before staging. No test pass → no stage → no swap. Test results go in the audit log.

**Agent capability for self-update**: `self_update`, `self_rollback`, `query_deploy_history` are MCP tools in the system category. Restricted by capability — only specific agent types (e.g. "ops", "lead") get them.

**Open sub-decisions**:
- **Sidecar image versioning**: tag per git SHA, per semver, or both? Lean SHA + `latest`
- **Schema migration safety**: only-additive discipline + tests, or explicit review gate?
- **`sextant doctor` companion**: reports component health + config paths; helpful for both operator and agents

## 13. Self E2E testing — DECIDED (ephemeral isolated test environments)

The pilot's problem: sandboxed agents could build sextant but not run real E2E tests, because E2E needs docker access, ports, filesystem write, signing-key issuance — perms that would compromise production if granted.

**Solution**: a test env is a complete parallel sextant deployment in a namespace separate from production. Agents have *full* permissions within a test env; production is unaffected.

### Namespacing model

| Component | Namespaced via |
|---|---|
| NATS | Separate port or unix socket (`~/.local/share/sextant/test/<uuid>/nats.sock`) |
| ClickHouse | Separate data dir + port |
| Docker containers | Name prefix `sextant-test-<uuid>-` |
| Signing CA | Test-only keypair, not trusted by production |
| Config dir | `~/.local/share/sextant/test/<uuid>/` |
| File paths | Test env's HOME ≠ production's HOME |

Shared (no isolation gain from separating):
- Docker daemon (one daemon, namespaced containers)
- Host OS
- Sidecar base image (immutable, safe to share)

### MCP tools (new "testing" category in §9c)

- `provision_test_environment(name?, ttl_minutes?=60, profile?="default")` → `{ test_id, nats_url, clickhouse_url, test_ca_pubkey, config_path }`
- `teardown_test_environment(test_id)` → ok
- `list_test_environments()` → `[...]`
- `connect_to_test_environment(test_id)` → connection handle for the client SDK

Capability-gated by `test_provision`. Production-running agents that shouldn't touch testing infra don't get this cap.

### Lifecycle

1. **Provision**: sextantd allocates UUID + ports, creates data dirs, generates test CA keypair, starts `nats-server`, `clickhouse-server`, and `sextantd --test-mode --test-id=<uuid>`. Registers env with TTL.
2. **Active**: agent connects via returned config path. Runs tests against real but isolated infrastructure. Can spawn test agents, deploy test sextantd binaries, exercise multi-host (multiple test envs), trigger failure modes.
3. **Teardown**: explicit (`teardown_test_environment`) or TTL expiry. Reaper kills processes, removes data dirs, frees ports, cleans up containers by namespace prefix.

### Reaper

Sextantd background goroutine:
- Tracks all envs in NATS KV (`test_envs.<uuid>` → metadata + TTL)
- Periodically force-cleans expired envs
- On sextantd shutdown, force-cleans all envs (no orphans)

### Test profiles

`profile` parameter parameterizes what gets stood up:
- `default` — full sextant deployment
- `minimal` — NATS + sextantd only (no ClickHouse/shipper)
- `multi_host` — 2-3 test envs with NATS clustering for federation tests
- Custom profiles in `~/.config/sextant/test-profiles/`

### Resource limits

Per-test-env quotas (max processes, max disk in test dir, max ports). Quotas enforced at provision time; over-quota requests fail.

### Recursion

Test envs spawning test envs is allowed but bounded (default depth limit 2). Useful for testing the test-env machinery itself.

### Typical flow

```
agent: provision_test_environment(ttl_minutes=30)
       → { test_id: "ABC", nats_url: "...", ... }
agent: connect to ABC's NATS via sextant-client-go
agent: spawn_agent(name="test-lead", ...) [against ABC]
agent: prompt test-lead, watch frames, assert on outcomes
agent: trigger self_update inside ABC, verify watchdog behavior
agent: teardown_test_environment("ABC")
```

Real NATS, real containers, real ClickHouse, real signing CA — all in a namespace. Real permissions, zero production risk.

### Open sub-decisions

- **Pre-warmed pool**: provisioning takes 5-10s (NATS + ClickHouse startup). Pool of N pre-warmed envs keeps provisioning sub-second. Lean: implement post-MVP if startup latency hurts.
- **Operator UI test-env attach**: operator's main TUI can inspect a running test env via admin capability. Useful for debugging weird test failures.
- **Cross-env communication**: test envs isolated by default; federation tests use the `multi_host` profile that explicitly clusters them.
- **Canonical test fixtures**: ship named seeds (sample audit logs, agent definitions) that envs can be initialized with.
- **Persistent test envs**: `persistent=true` flag (operator-cap) for long-running soak tests that should outlive default TTL.

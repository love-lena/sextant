# Sidecar container image — component spec

## Role

The base container image every agent runs in. Contents:
- Node.js (LTS) + Claude Code Agent SDK
- `@sextant/client` (TS) for talking to NATS
- Sidecar runtime entrypoint (manages SDK loop, publishes events, subscribes to control)
- Rich tool set for agent productivity

See `architecture.md` §3 (sandbox) and §1 (runtime adapter).

## Image tag

`sextant-sidecar:<git-sha>` per build, plus a `latest` tag pointing at the most-recent successful build.

Built via `make sidecar-image` which runs `docker build -f images/sidecar/Dockerfile -t sextant-sidecar:$(git rev-parse HEAD) -t sextant-sidecar:latest .`

## Base

Debian Bookworm slim. Rationale: stable, well-supported, has all the packages we want without Alpine's musl quirks.

## Installed tools

| Category | Tools |
|---|---|
| **Sextant runtime** | Node 22 LTS (current "Jod" line), `@sextant/client`, `@anthropic-ai/claude-agent-sdk`, sidecar entrypoint |
| **VCS** | git, gh |
| **Search/text** | ripgrep, fzf, jq, yq |
| **HTTP** | curl, wget, httpie |
| **Build tools** | make, gcc, pkg-config (for native deps) |
| **Languages** | Go 1.26+ (latest stable, downloaded as the official tarball at image build time), Node 22 + npm, Python 3 + pip |
| **Editors** | vim (for the rare case an agent wants to invoke it interactively) |
| **OS basics** | bash, coreutils, less, procps, sudo (no password — agent owns the container) |

### Version pins — M9 refinements

Every externally-fetched component is pinned to an exact version via Dockerfile `ARG`. Cache-cold rebuilds produce the same image; bumps are deliberate ARG changes committed alongside an updated pin here. **Do not use floating tags like `@latest` or `releases/latest/download` in the Dockerfile** — they make the image non-reproducible and turn every silent upstream change into a diff in production.

- **Node 22 LTS** (active LTS line as of M9; spec previously said "Node 20+ LTS"). Installed via the NodeSource apt repo, pinning the major (`nodejs_22.x` — NodeSource resolves the latest patch in the line at apt-update time, which is acceptable since the major+minor lock is the API-stability boundary that matters).
- **Go**: downloaded directly from `https://go.dev/dl/` at image build time using the URL pattern `https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz`, extracted to `/usr/local/go`. `GO_VERSION` is a build arg defaulting to `1.26.3` (current stable as of M9). The `arch` is resolved from `dpkg --print-architecture` so the image builds on both `amd64` and `arm64` (OrbStack on Apple Silicon).
- **Claude Code Agent SDK**: `@anthropic-ai/claude-agent-sdk@0.3.150` — latest stable on npm at M9 pin time (`npm view @anthropic-ai/claude-agent-sdk version`). Pinned via `ARG CLAUDE_AGENT_SDK_VERSION`.
- **Claude Code CLI**: `@anthropic-ai/claude-code@2.1.150` — latest stable on npm at M9 pin time. Pinned via `ARG CLAUDE_CODE_VERSION`. Installed globally so the sidecar can shell out to `claude` interactively in addition to driving the SDK programmatically.
- **yq**: `v4.53.2` — latest stable GitHub release at M9 pin time (`gh api repos/mikefarah/yq/releases/latest`). Pinned via `ARG YQ_VERSION`; the Dockerfile downloads from `https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${arch}` (no more `releases/latest/download`).
- **`@sextant/client` source**: not yet published to npm. The Dockerfile resolves it via a local file dependency (`"@sextant/client": "file:../client-ts"`) where `client-ts/` is a tarball-equivalent copy of `clients/typescript/` (with `node_modules/` excluded) staged into the build context by the Dockerfile via `COPY clients/typescript /opt/sextant/client-ts`. When `@sextant/client` is published, switch to the registry version with an exact pin and drop the COPY.

**Bump procedure**: for each ARG, update the Dockerfile default in the same commit as the matching version string above. Reference the source you queried (`npm view ... version` for npm packages, GitHub releases API for yq/Go) in the commit body so the next pin bump can verify against the same source.

Intentionally omitted:
- Rust — sextant is Go; agents working on sextant don't need rustup
- Heavy dev tools (full Xcode-equivalents, IDEs)

## Sidecar entrypoint

Located at `/opt/sextant/sidecar/`. Started by the container's `CMD`.

Responsibilities:
1. Read agent UUID, NATS URL, JWT, session_id, MCP URL from env vars
2. Connect to NATS via `@sextant/client` (TCP listener with JWT)
3. Connect to MCP server over Streamable HTTP at `SEXTANT_MCP_URL` (default `http://host.docker.internal:5172/mcp`); present the JWT as `Authorization: Bearer <SEXTANT_JWT>` on every request. See `specs/architecture.md` §9c "MCP transport".
4. Start Claude Code SDK with the session_id (`--resume` if provided) and the MCP server URL
5. Capture SDK events → publish as bus frames
6. Subscribe to `agents.<uuid>.inbox` → forward prompts/commands to SDK
7. Publish heartbeat to `agents.<uuid>.heartbeat` every N seconds
8. On SDK exit: publish `lifecycle.ended` event with reason; container exits (the `LifecycleEnded` constant in `pkg/sextantproto/payloads.go`; the entrypoint publishes this on SIGTERM / SIGINT, and `lifecycle.turn_ended` after each individual turn).

### Scope progression

- **M9** (scaffold): image + env-var contract + `lifecycle.started` / heartbeat / `lifecycle.ended`.
- **M10**: MCP server side bound. Sidecar opens the MCP client connection over Streamable HTTP at `SEXTANT_MCP_URL`, presenting `SEXTANT_JWT` as the Bearer token. The MCP server verifies the JWT against the M5 CA and enforces per-tool capability checks.
- **M11**: spawn flow + per-incarnation JWT. Sidecar subscribes to `agents.<uuid>.inbox`. Operator-creds NATS auth (`SEXTANT_NATS_USER` / `SEXTANT_NATS_PASSWORD`) per `specs/components/nats.md` §"Agent path" — the JWT remains MCP-only at this stage.
- **Post-Phase-1 (`feat-sidecar-sdk-driver-001`)**: Claude Agent SDK driver loop is wired. On each inbox prompt the sidecar invokes `@anthropic-ai/claude-agent-sdk` `query()`, streams its events as `agent_frame` envelopes to `agents.<uuid>.frames`, and publishes a `lifecycle.turn_ended` envelope when the turn completes. The first turn's SDK-issued `session_id` is persisted back to the `agent_definitions.<uuid>` KV entry (`runtime.session_id`); subsequent spawns of the same agent set `SEXTANT_SESSION_ID` to resume that session. A `--driver=mock` mode is provided for tests (canned events, no API call).

The entrypoint exits with a clear error if required NATS credentials are missing.

## Volume mounts (set by sextantd at spawn)

| Container path | Source | Mode |
|---|---|---|
| `/workspace` | agent's worktree on host | rw |
| `/home/agent/.claude` | per-agent named volume | rw |
| `/home/agent/.cargo`, `/home/agent/.npm`, `/home/agent/.cache`, `/home/agent/.local/share` | per-agent named volumes | rw |
| `/home/agent/.config/gh`, `/home/agent/.gitconfig` | per-agent or host-bind (declared per agent) | per-agent declaration |
| `/run/sextant/nats.sock` | host's NATS socket | rw |
| `/home/agent/.ssh` | host's `~/.ssh` directory (opt-in via template `mounts = [..., "ssh"]`) | **ro** |

**SSH passthrough** is strictly opt-in. The default and lead templates do **not** declare the `ssh` mount class; only operators who trust an agent class enough to share their personal SSH identity should add `"ssh"` to that template's `mounts` field. When present, sextantd bind-mounts the host's `~/.ssh` (resolved via `os.UserHomeDir()`) read-only at `/home/agent/.ssh`, which is enough for the agent to run `ssh -T git@github.com` and `git push` over SSH without the keys being writable from inside the sandbox. See `slug:feat-container-ssh-passthrough` and `architecture.md` §11b "Mount classes" for the full rationale.

## Env vars (set by sextantd at spawn)

- `SEXTANT_AGENT_UUID`
- `SEXTANT_AGENT_NAME`
- `SEXTANT_NATS_URL`
- `SEXTANT_JWT` — per-incarnation JWT signed by the M5 CA. Consumed by the **MCP** transport (Bearer token); see `specs/components/sextantd.md` §"MCP server" and `architecture.md` §9c. Not consumed by NATS at M11 — see `specs/components/nats.md` §"Config" for the per-agent NATS auth deferral.
- `SEXTANT_NATS_USER`, `SEXTANT_NATS_PASSWORD` (M11+) — operator NATS credentials, set by sextantd at spawn so the sidecar can connect to NATS. This is the explicit M11 stop-gap for per-agent NATS authentication; the eventual per-incarnation NATS-user promotion drops these in favor of the JWT being consumed directly by NATS. Documented in `specs/components/nats.md` §"Config".
- `SEXTANT_SESSION_ID` (optional; if set, sidecar passes it to the SDK as `resume`)
- `SEXTANT_MCP_URL` (M11+ — sidecar wires MCP)
- `SEXTANT_HOST_ID`
- `SEXTANT_INCARNATION_ID` (M11+) — sidecar uses this as the lifecycle/heartbeat `incarnation_id` so envelopes match the KV record sextantd wrote. If unset (older spawns, tests) the sidecar generates a UUID and that becomes the de-facto incarnation ID for the run.
- `SEXTANT_MODEL` (post-Phase-1 SDK wire-up) — Claude model identifier passed to the Agent SDK. Spawn handler resolves this from the agent template's `model` field, defaulting to `claude-opus-4-7[1m]` (per `architecture.md` §11b) when the template doesn't set it.
- `ANTHROPIC_API_KEY` (post-Phase-1 SDK wire-up) — pass-through of the operator's API key so the Claude Agent SDK can authenticate against the Anthropic API. Resolved at spawn time from sextantd's own process environment (the operator exports it before starting sextantd). Forwarded verbatim into the container env; not stored in KV. Falls through to the SDK's default credential resolution when unset — see `architecture.md` §3 "Credentials & secrets" for the longer-term `credentials` block.

Plus any per-agent credentials declared in the agent definition.

## Networking

Default: open egress. The container can reach the internet, the host's NATS, anything else.

Per-agent egress restrictions (future): declared in agent definition; sextantd configures container's network namespace accordingly. Not in scope for initial.

## Open

- Image size budget — aim for < 2GB
- Should we ship multiple base variants (minimal, default, heavy)? Probably not for initial.
- Pre-installing common npm/go/pip packages — useful or premature? Lean: minimal package installs in the image; agents install what they need via cached volumes.

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
8. On SDK exit: publish `lifecycle.session_ended` event with reason; container exits

### M9 scope (scaffold only)

M9 ships the image and a *scaffolded* entrypoint that satisfies the image-build acceptance and proves the env-var contract. The full responsibility set above lands progressively:

- **JWT-authenticated NATS connection** lands at M11 (M8's TS client only supports password auth; the JWT/credsPath path is explicitly NotYetSupported). For M9, if `SEXTANT_OPERATOR_USER`/`SEXTANT_OPERATOR_PASSWORD` are set, the entrypoint dials NATS via password auth; if only `SEXTANT_JWT` is set, it logs the M11 gap and stays in a no-NATS heartbeat loop.
- **MCP connection** is not opened by the M9 entrypoint (M10 ships the MCP server; M11 wires the sidecar to it).
- **Claude Code SDK invocation** lands at M11. M9 ships a `lifecycle.started` publish + 5-second heartbeat publish loop and a clean `lifecycle.ended` on SIGTERM. That is sufficient to prove the image+entrypoint integration; the SDK loop is bolted on without changing the surrounding contract.
- The M9 entrypoint exits with a clear error if neither operator credentials nor a JWT env var are present, listing what M11 will add.

## Volume mounts (set by sextantd at spawn)

| Container path | Source | Mode |
|---|---|---|
| `/workspace` | agent's worktree on host | rw |
| `/home/agent/.claude` | per-agent named volume | rw |
| `/home/agent/.cargo`, `/home/agent/.npm`, `/home/agent/.cache`, `/home/agent/.local/share` | per-agent named volumes | rw |
| `/home/agent/.config/gh`, `/home/agent/.gitconfig` | per-agent or host-bind (declared per agent) | per-agent declaration |
| `/run/sextant/nats.sock` | host's NATS socket | rw |

## Env vars (set by sextantd at spawn)

- `SEXTANT_AGENT_UUID`
- `SEXTANT_AGENT_NAME`
- `SEXTANT_NATS_URL`
- `SEXTANT_JWT` (M11+; for M9 the entrypoint accepts but does not yet use it — see "M9 scope" above)
- `SEXTANT_SESSION_ID` (optional; if set, sidecar starts SDK with `--resume`)
- `SEXTANT_MCP_URL` (M11+ — sidecar wires MCP)
- `SEXTANT_HOST_ID`
- `SEXTANT_OPERATOR_USER`, `SEXTANT_OPERATOR_PASSWORD` (M9-only: lets test runs/the M9 smoke connect over the operator password path while M11's JWT path is still under construction; not set by sextantd at spawn from M11 onwards)

Plus any per-agent credentials declared in the agent definition.

## Networking

Default: open egress. The container can reach the internet, the host's NATS, anything else.

Per-agent egress restrictions (future): declared in agent definition; sextantd configures container's network namespace accordingly. Not in scope for initial.

## Open

- Image size budget — aim for < 2GB
- Should we ship multiple base variants (minimal, default, heavy)? Probably not for initial.
- Pre-installing common npm/go/pip packages — useful or premature? Lean: minimal package installs in the image; agents install what they need via cached volumes.

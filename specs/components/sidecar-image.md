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
| **Sextant runtime** | Node 20+ LTS, `@sextant/client`, sidecar entrypoint |
| **VCS** | git, gh |
| **Search/text** | ripgrep, fzf, jq, yq |
| **HTTP** | curl, wget, httpie |
| **Build tools** | make, gcc, pkg-config (for native deps) |
| **Languages** | Go (latest stable), Node + npm, Python 3 + pip |
| **Editors** | vim (for the rare case an agent wants to invoke it interactively) |
| **OS basics** | bash, coreutils, less, procps, sudo (no password — agent owns the container) |

Intentionally omitted:
- Rust — sextant is Go; agents working on sextant don't need rustup
- Heavy dev tools (full Xcode-equivalents, IDEs)

## Sidecar entrypoint

Located at `/opt/sextant/sidecar/`. Started by the container's `CMD`.

Responsibilities:
1. Read agent UUID, NATS URL, JWT, session_id from env vars
2. Connect to NATS via `@sextant/client`
3. Connect to MCP server (sextantd's MCP endpoint over NATS)
4. Start Claude Code SDK with the session_id (`--resume` if provided) and the MCP server URL
5. Capture SDK events → publish as bus frames
6. Subscribe to `agents.<uuid>.inbox` → forward prompts/commands to SDK
7. Publish heartbeat to `agents.<uuid>.heartbeat` every N seconds
8. On SDK exit: publish `lifecycle.session_ended` event with reason; container exits

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
- `SEXTANT_JWT`
- `SEXTANT_SESSION_ID` (optional; if set, sidecar starts SDK with `--resume`)
- `SEXTANT_MCP_URL`
- `SEXTANT_HOST_ID`

Plus any per-agent credentials declared in the agent definition.

## Networking

Default: open egress. The container can reach the internet, the host's NATS, anything else.

Per-agent egress restrictions (future): declared in agent definition; sextantd configures container's network namespace accordingly. Not in scope for initial.

## Open

- Image size budget — aim for < 2GB
- Should we ship multiple base variants (minimal, default, heavy)? Probably not for initial.
- Pre-installing common npm/go/pip packages — useful or premature? Lean: minimal package installs in the image; agents install what they need via cached volumes.

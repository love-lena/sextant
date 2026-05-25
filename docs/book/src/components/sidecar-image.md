# Sidecar image

**Source**: `images/sidecar/`.

The sidecar image is the Docker base every agent container runs from. Each container is one running `node /opt/sextant/sidecar/dist/index.js run`, which is the TypeScript entrypoint that drives the Claude Agent SDK and bridges it to the sextant bus.

## When to reach for this component

- You want to know what's installed in an agent container.
- You're changing the entrypoint's behaviour: prompt handling, frame translation, MCP wiring, shutdown.
- You're adding a tool-permission rule.

## Base + tags

- Base: `debian:bookworm-slim` (`images/sidecar/Dockerfile`).
- Tags built by `make sidecar-image`: `sextant-sidecar:<git-sha>` and `sextant-sidecar:latest` (`Makefile:153-157`).
- Image size: target `< 2 GiB`; the smoke test at `images/sidecar/test.sh:70-75` **warns** (does not fail) when the image exceeds 3 GiB.

## Bundled tools

| Category            | Tools                                                                                  |
|---------------------|----------------------------------------------------------------------------------------|
| Runtime             | Node 22 LTS, npm, `@anthropic-ai/claude-agent-sdk`, `@anthropic-ai/claude-code`        |
| VCS                 | `git`, `gh`                                                                            |
| Search / text       | `ripgrep`, `fzf`, `jq`, `yq`                                                            |
| HTTP                | `curl`, `wget`, `httpie`                                                                |
| Build               | `make`, `gcc`, `pkg-config`                                                             |
| Languages           | Go 1.26.3, Python 3 + pip                                                               |
| Editor              | `vim`                                                                                   |
| OS basics           | `bash`, `coreutils`, `less`, `procps`, `sudo`                                           |

`images/sidecar/test.sh:42-51` asserts presence of fifteen of these on PATH (`node npm git gh jq yq rg fzf curl wget make gcc python3 go vim`). The rest are installed by the Dockerfile but not explicitly probed by the smoke test.

## Entrypoint shell script

`images/sidecar/entrypoint/entrypoint.sh` validates that every required env var is set, then execs `node /opt/sextant/sidecar/dist/index.js run`. Missing required vars cause `exit(1)` before Node even starts.

## Container environment variables

Set by `sextantd`'s spawn handler. Read by `images/sidecar/entrypoint/src/index.ts:99-124`.

| Variable                      | Required | Source                                     |
|-------------------------------|----------|--------------------------------------------|
| `SEXTANT_AGENT_UUID`          | yes      | Agent definition.                          |
| `SEXTANT_AGENT_NAME`          | yes      | Agent definition.                          |
| `SEXTANT_HOST_ID`             | yes      | `sextantd` host identifier.                |
| `SEXTANT_INCARNATION_ID`      | yes      | Per-spawn identifier.                      |
| `SEXTANT_NATS_URL`            | yes      | NATS TCP URL (via `host.docker.internal`). |
| `SEXTANT_NATS_USER`           | yes      | M11 transitional: operator user.           |
| `SEXTANT_NATS_PASSWORD`       | yes      | M11 transitional: operator password.       |
| `SEXTANT_JWT`                 | optional | Per-incarnation JWT for MCP `Authorization`. The sidecar tolerates absence and continues without MCP. |
| `SEXTANT_MCP_URL`             | optional | `http://host.docker.internal:5172/mcp`.    |
| `SEXTANT_SESSION_ID`          | optional | If present, SDK resumes that session.      |
| `SEXTANT_MODEL`               | optional | Default `claude-opus-4-7[1m]` (`index.ts:62`). |
| `SEXTANT_PERMISSION_MODE`     | optional | `acceptEdits` / `plan` / `default`.        |
| `SEXTANT_INITIAL_PROMPT`      | optional | Base64-encoded systemPrompt for every turn.|

> **M11 transitional auth**: the sidecar connects to NATS as the operator user, not under its own NATS user. The JWT is consumed *only* by the MCP server, not by NATS. The architecture spec (§10a) notes this as a known stop-gap; per-NATS-user JWTs are a future hardening pillar.

## Sidecar entrypoint flow

1. `readEnv()` parses and validates env (`index.ts:99-124`).
2. Connect to NATS via `@sextant/client.connect`.
3. Optionally connect to the MCP server over Streamable HTTP. If `SEXTANT_MCP_URL` and `SEXTANT_JWT` are both set, attempt; on either being missing or on connect failure, log and continue without MCP (the agent can still observe NATS even with no tools).
4. Publish `lifecycle.started` to `agents.<uuid>.lifecycle`.
5. Subscribe to `agents.<uuid>.inbox` with `deliverAll: true`. Each prompt is pushed onto a serial `PromptQueue`.
6. Start a 5-second heartbeat ticker on `agents.<uuid>.heartbeat`.
7. On each prompt, invoke the driver (`--driver=sdk` by default; `--driver=mock` for tests). The SDK driver calls `query()` from `@anthropic-ai/claude-agent-sdk` with `model`, `permissionMode`, `systemPrompt` (decoded from `SEXTANT_INITIAL_PROMPT`), and the `mcpServers.sextant` block.
8. Each SDK message becomes an `agent_frame` envelope (`frame_kind`: `assistant_text`, `tool_call`, `tool_result`, `system_note`, `error`).
9. After the SDK loop completes, publish `lifecycle.turn_ended` and persist the SDK's `session_id` to `agent_definitions.<uuid>.runtime.session_id` with compare-and-set.
10. On `SIGTERM`/`SIGINT`: stop new prompts, settle in-flight turn up to 5 s, wait up to 2 s for the heartbeat tick, publish `lifecycle.ended` with `reason=signal:SIG*`, close MCP and NATS clients, `exit(0)`.

## Tool-permission classifier

Registered as the SDK's `canUseTool` callback (`index.ts:565-577`). Decisions live in `images/sidecar/entrypoint/src/classifier.ts:130-156`:

- **Safe tools** (auto-allowed): `Edit`, `Write`, `MultiEdit`, `Read`, `Glob`, `Grep`, `TodoWrite`, `NotebookEdit`.
- **Bash**: allowed unless `isDangerousBashCommand` matches. The deny patterns (`classifier.ts:51-100`) cover:
  - `sudo` (containers don't need it)
  - `rm -rf /` (anchored)
  - `rm -rf ~` / `rm -rf $HOME` (anchored)
  - `rm -rf /workspace`
  - `dd if=/dev/{zero,random}`
  - `mkfs.*`
  - The literal fork bomb `:(){:|:&};:`
  - `curl|sh` and `wget|bash` patterns, including pipes through `tee` (so `curl … | tee /tmp/x | bash` is still denied)
- **`mcp__*` tools**: allowed locally. Server-side capability checks gate them.
- **Everything else**: denied as an unknown tool.

Test coverage: `images/sidecar/entrypoint/test/classifier.test.ts` (28 safe-command cases and 26 dangerous-command cases in the data tables, plus integration assertions for the rm-rf flag permutations and the curl multi-pipe bypass).

## Frame publishing

Each SDK message is translated by `handleSDKMessage` (`index.ts:639-745`). The five `frame_kind` values:

| `frame_kind`    | When                                                  |
|-----------------|-------------------------------------------------------|
| `assistant_text`| Assistant text chunks                                 |
| `tool_call`     | The SDK invoked a tool                                |
| `tool_result`   | A tool returned                                       |
| `system_note`   | Initialization, end-of-turn notes                     |
| `error`         | Any error path                                        |

Each is published on `agents.<uuid>.frames` as a `KIND_AGENT_FRAME` envelope.

## Session persistence

`persistSessionID(ctx, sessionId)` (`index.ts:356-403`):

1. Fetch `agent_definitions.<uuid>` from KV.
2. If `runtime.session_id` already matches, do nothing.
3. Otherwise `update` the entry with CAS against the current revision.
4. On CAS conflict, retry once.

Persistence is **best-effort** — failure is logged but doesn't block the agent. The `session_id` is included on every published frame so the bus is the durable record anyway.

## Smoke test

`images/sidecar/test.sh` is the M9 acceptance smoke. It builds the image, verifies every required tool is on PATH, verifies the entrypoint binary exists, and checks the image size limit. CI runs this on every change to the image.

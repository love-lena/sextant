# `@sextant/sidecar` — per-agent container runtime

The TypeScript runtime that boots inside every per-agent container, bridging the Claude Code Agent SDK to the sextant bus.

Spec: [`specs/components/sidecar-image.md`](../../../specs/components/sidecar-image.md). Wire-up: [`plans/wire-up-complete.md`](../../../plans/wire-up-complete.md).

## What this sidecar does

On boot the sidecar:

1. Reads the env-var contract from `specs/components/sidecar-image.md` §"Env vars".
2. Connects to NATS via `@sextant/client` using the broker-scoped **sidecar** credentials sextantd forwards (`SEXTANT_NATS_USER=sidecar`; feat-ctl-f0). The sidecar principal may publish only `agents.*.{frames,heartbeat,lifecycle}` and subscribe `agents.*.inbox` — it cannot reach the RPC front door or publish prompts to other agents. (Per-incarnation NATS-JWT auth narrowing this to the single `agents.<uuid>.*` is the long-term replacement.)
3. Connects to the sextantd MCP server over Streamable HTTP at `SEXTANT_MCP_URL`, presenting `SEXTANT_JWT` as the bearer token.
4. Publishes `lifecycle.started` on `agents.<uuid>.lifecycle`.
5. Publishes a `HeartbeatPayload` every 5 seconds on `agents.<uuid>.heartbeat`.
6. Subscribes to `agents.<uuid>.inbox` and on each prompt:
   - Invokes `@anthropic-ai/claude-agent-sdk` `query()` with the configured model + the operator's bearer-authenticated sextantd MCP server as a tool source.
   - Streams the SDK's message events as `agent_frame` envelopes on `agents.<uuid>.frames` (`frame_kind=assistant_text` for model text, `tool_call` for `tool_use` blocks, `tool_result` for the user-message wrapper the SDK synthesizes around tool results, `system_note` for SDK system events, `error` for SDK / publish failures).
   - On turn end (success or error) publishes `lifecycle.turn_ended` on `agents.<uuid>.lifecycle`.
   - Captures the SDK's `session_id` and writes it back to `agent_definitions.<uuid>` (NATS KV bucket `agent_definitions`, field `runtime.session_id`) so subsequent spawns of the same agent resume the same session via `SEXTANT_SESSION_ID`.
7. On `SIGTERM` / `SIGINT`: stops accepting new prompts, waits up to 5s for the in-flight turn to finish, publishes `lifecycle.ended`, closes NATS + MCP, exits 0.

Concurrent prompts arriving on the inbox are serialized via an in-process queue — one SDK turn at a time per incarnation.

## Driver modes

`sextant-sidecar run --driver=sdk` (the default) drives the real Claude Agent SDK against the Anthropic API. `--driver=mock` (also selectable via `SEXTANT_DRIVER=mock`) substitutes a canned-event driver that emits an `assistant_text` frame echoing the prompt + a `lifecycle.turn_ended` envelope, without an API call. The mock mode is used by `cmd/sextantd`'s integration test for the SDK driver loop so CI doesn't depend on Anthropic credentials; the live mode is exercised by the manual smoke walkthrough captured in `plans/wire-up-complete.md`.

## API key plumbing

The real SDK needs `ANTHROPIC_API_KEY`. The spawn handler reads it from sextantd's own process environment and forwards it verbatim into the container at spawn time (see `pkg/rpc/handlers/spawn.go` and `specs/components/sidecar-image.md` §"Env vars"). When sextantd is launched without the env var set the sidecar falls back to the SDK's default credential resolution, which on macOS picks up the operator's `claude` CLI login — sufficient for manual smoke runs but not for unattended operation.

The longer-term `credentials` block in agent definitions (per `specs/architecture.md` §3) is the eventual hardening path; this is the simplest defensible interim.

## Running locally (outside the container)

```bash
cd images/sidecar/entrypoint
npm install
npm run build
SEXTANT_AGENT_UUID=$(uuidgen) \
SEXTANT_AGENT_NAME=test \
SEXTANT_HOST_ID=local \
SEXTANT_INCARNATION_ID=$(uuidgen) \
SEXTANT_NATS_URL=nats://127.0.0.1:4222 \
SEXTANT_NATS_USER=sidecar \
SEXTANT_NATS_PASSWORD=<sidecar password> \
SEXTANT_MODEL=claude-opus-4-7 \
node dist/index.js run --driver=mock
```

Since feat-ctl-f0 the sidecar + daemon NATS passwords are boot-generated
and not written to a creds file. For a manual smoke run, start NATS with
`sextant-natsboot` and copy the printed `sidecar password` (the `daemon`
password also works for a privileged manual run). Under normal operation
sextantd forwards these env vars into the container automatically.

In production this is invoked by `entrypoint.sh` (the image's CMD when sextantd spawns an agent).

## Layout

```
entrypoint/
├── package.json          # name: @sextant/sidecar (private)
├── tsconfig.json
├── entrypoint.sh         # validates env, execs node dist/index.js run
├── src/
│   └── index.ts          # the runtime
└── README.md             # this file
```

## Build

`npm run build` runs `tsc -p tsconfig.json` and emits `dist/index.js`. The Dockerfile runs this inside the image; locally you can run it with a recent Node 22 LTS.

## Known caveats

- **Partial / streaming assistant chunks are not forwarded.** The SDK emits one full `assistant` message per round-trip; we publish its content blocks as `agent_frame` envelopes from there. `SDKPartialAssistantMessage` events are dropped to keep the bus volume manageable. Re-evaluate if a consumer needs token-by-token rendering.
- **Tool-result frames piggyback on the SDK's synthetic user message.** The SDK rewrites tool results into a user message before feeding them back to the model; we publish a `tool_result` frame from that. The original `tool_result` block on the model side is therefore the SDK's normalization of the tool output, not the raw MCP response — close enough for the bus transcript, but worth knowing when correlating with `audit.tool_call` envelopes.
- **Session-id persistence is best-effort.** A KV write failure logs and continues; the next prompt simply mints a new session. Bundled spawn flows that need durable resume should treat the published session_id (visible on every `agent_frame`) as the source of truth.

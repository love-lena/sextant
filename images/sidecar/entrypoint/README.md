# `@sextant/sidecar` — per-agent container runtime

The TypeScript runtime that boots inside every per-agent container, bridging the Claude Code Agent SDK to the sextant bus.

Plan: [`plans/bootstrap.md#M9`](../../../plans/bootstrap.md#m9). Spec: [`specs/components/sidecar-image.md`](../../../specs/components/sidecar-image.md).

## M9 scope

This package ships as a *scaffold* in M9. It proves the container surface (image build, env-var contract, NATS connect, lifecycle event published) so M10 (MCP server) and M11 (spawn flow + JWT + SDK loop) can bolt onto a known-good substrate.

What the scaffold does:

1. Read the env-var contract from `specs/components/sidecar-image.md` §"Env vars".
2. Connect to NATS via `@sextant/client`.
3. Publish `lifecycle.started` on `agents.<uuid>.lifecycle`.
4. Publish a `HeartbeatPayload` every 5 seconds on `agents.<uuid>.heartbeat`.
5. On `SIGTERM` / `SIGINT`: publish `lifecycle.ended`, close the client, exit 0.

What the scaffold does **not** do (and where each piece lands):

| Capability | Lands in |
|---|---|
| Per-incarnation JWT auth to NATS | M11 |
| MCP server connection (`SEXTANT_MCP_URL`) | M11 (M10 ships the server) |
| Claude Code Agent SDK driver loop | M11 |
| `agents.<uuid>.inbox` subscription / prompt routing | M11 |
| Agent frame publish on `agents.<uuid>.frames` | M11 |

## NATS auth — M9 vs M11

The TS client (`@sextant/client`, M8) only supports the operator *password* auth path; the `credsAuthenticator` / JWT wiring is explicitly NotYetSupported until M11. To exercise the M9 entrypoint end-to-end the sidecar therefore accepts a temporary operator credentials pair:

```
SEXTANT_OPERATOR_USER=operator
SEXTANT_OPERATOR_PASSWORD=<from operator.creds>
```

M11 swaps this for `SEXTANT_JWT` (per-incarnation JWT signed by the M5 CA) and `SEXTANT_OPERATOR_*` is dropped from the spawn-time env. The M9 entrypoint logs a clear message if `SEXTANT_JWT` is set without an operator password explaining where to look.

## Running locally (outside the container)

```bash
cd images/sidecar/entrypoint
npm install
npm run build
SEXTANT_AGENT_UUID=$(uuidgen) \
SEXTANT_AGENT_NAME=test \
SEXTANT_HOST_ID=local \
SEXTANT_NATS_URL=nats://127.0.0.1:4222 \
SEXTANT_OPERATOR_USER=operator \
SEXTANT_OPERATOR_PASSWORD=$(cat ~/.config/sextant/operator.creds) \
node dist/index.js run
```

In production this is invoked by `entrypoint.sh` (the image's CMD when sextantd spawns an agent).

## Layout

```
entrypoint/
├── package.json          # name: @sextant/sidecar (private)
├── tsconfig.json
├── entrypoint.sh         # validates env, execs node dist/index.js
├── src/
│   └── index.ts          # the runtime
└── README.md             # this file
```

## Build

The package's `npm run build` runs `tsc -p tsconfig.json` and emits `dist/index.js`. The Dockerfile runs this inside the image; locally you can run it with a recent Node 22 LTS.

# TypeScript client (`@sextant/client`)

**Source**: `clients/typescript/`.

The TypeScript client mirrors the Go client API. It is the library every agent sidecar uses to talk to the sextant bus, and the recommended way to build TS-side tooling.

## When to reach for this component

- You're working on the sidecar entrypoint.
- You're building a TS-side TUI or a one-shot script.
- You need to know what regenerates `types.generated.ts`.

## Public surface

| Symbol                                       | File:line                                | Purpose                                                  |
|----------------------------------------------|------------------------------------------|----------------------------------------------------------|
| `connect(opts?)`                             | `clients/typescript/src/client.ts:254`   | Auto-load `~/.config/sextant/client.toml` and dial NATS. |
| `connectWithConfig(cfg, opts?)`              | `clients/typescript/src/client.ts:264`   | Same, but skip the file load.                            |
| `Client.close()`                             | `:221`                                   | Idempotent disconnect.                                   |
| `Client.config`                              | `:59`                                    | Effective config.                                        |
| `Client.rpc<Req,Resp>(verb, req, opts?)`     | `clients/typescript/src/rpc.ts:51`       | Typed request/reply.                                     |
| `Client.publish(subject, env)`               | `clients/typescript/src/client.ts:169`   | Publish a `sextantproto.Envelope`.                       |
| `Client.subscribe(subject, opts?)`           | `:161`                                   | `AsyncIterable<Message>`.                                |
| `Client.subscribeFromSeq(subject, fromSeq)`  | `:165`                                   | Resume from a JetStream sequence.                        |
| `Client.query(filter)`                       | `:181`                                   | History via `query_history`.                             |
| `Client.getKV` / `putKV` / `updateKV`        | `:189, 197, 205`                         | KV operations.                                           |
| `Client.watchKV(bucket, key)`                | `:185`                                   | `AsyncIterable<KVUpdate>`.                               |
| `loadConfig(filePath)`                       | `clients/typescript/src/config.ts:188`   | Parse `client.toml`; expand `~/`.                        |
| `validateAndFill(input)`                     | `:129`                                   | Default-fill and validate.                               |

## Configuration

Same schema as the Go client — `client.toml` is shared. The TS struct is `ClientConfig` (`clients/typescript/src/config.ts:46`).

| Field                       | Default        | Purpose                                          |
|-----------------------------|----------------|--------------------------------------------------|
| `nats.url`                  | none           | NATS URL.                                        |
| `operator.user`             | `"operator"`   | NATS username.                                   |
| `operator.password`         | unset          | Inline password.                                 |
| `operator.credsPath`        | unset          | Path to a creds file (currently a stub on TS).   |
| `client.connectTimeoutMs`   | `10000`        | Connect timeout.                                 |
| `client.requestTimeoutMs`   | `30000`        | Default RPC timeout (`clients/typescript/src/config.ts:166`). |
| `client.logLevel`           | `"info"`       | nats.js log level.                               |

> **Sidecar consumption**: the sidecar doesn't load `client.toml`; it builds a `ClientConfig` from env vars (`SEXTANT_NATS_URL`, `SEXTANT_NATS_USER`, `SEXTANT_NATS_PASSWORD`) and passes it to `connectWithConfig`.

> **Creds-file mode**: the TS client accepts `credsPath` in its config but **throws** if you try to connect with it set (`clients/typescript/src/client.ts:140-142` raises `"client: operator.credsPath is not yet supported..."`). Inline `user`/`password` is the only supported path on TS today.

> **`runtime.json` override**: not implemented on the TS side. The TS client uses whatever URL the caller (or `client.toml`) supplies. The sidecar always gets a real URL via `SEXTANT_NATS_URL`, so this isn't a blocker in practice.

## RPC ergonomics

```ts
import { connect } from "@sextant/client";

const c = await connect();
const resp = await c.rpc<ListAgentsRequest, ListAgentsResponse>(
  "list_agents", {}
);
```

Options on `rpc`:

- `timeoutMs` — override the 10-second default.
- `idempotencyKey` — server-side caching keyed on `(verb, key)`.

Errors:

- `RPCError(code, message, details?)` — typed server-side errors.
- `RPCTimeoutError` — client-side timeout.

## Subscribe ergonomics

```ts
for await (const m of c.subscribe("agents.*.frames", { deliverAll: true })) {
  console.log(m.subject, m.envelope.kind);
}
```

The iterator yields `Message { envelope, subject, streamSeq, consumerSeq, timestamp }`. Cancelling the iteration cleans up the consumer.

## Generated types

`clients/typescript/src/types.generated.ts` is auto-generated from `pkg/sextantproto/schemas/*.json`. Regenerate with:

```bash
make ts-codegen   # or: cd clients/typescript && npm run codegen
```

The generator (`clients/typescript/scripts/codegen.ts`) merges every per-handler schema's `$defs` into a single schema, rewrites `uuid`/`timestamp` formats to `string`, and runs `json-schema-to-typescript`. Re-running on the same input is a no-op.

`types.generated.ts` exports:

- Request/response interfaces for every RPC verb.
- `Envelope`, `AgentDefinition`, `AgentIncarnation`, etc.
- Frame-payload interfaces and OTel-shaped types.

## Reconnection knobs

The TS client pins these nats.js reconnect options (`clients/typescript/src/client.ts:128-131`):

```
maxReconnectAttempts: -1     // unlimited
reconnectTimeWait:    500 ms
reconnectJitter:      100 ms
reconnectJitterTLS:   500 ms
```

The Go client's `ReconnectJitter(min, max)` API maps loosely to the TS `reconnectJitter` / `reconnectJitterTLS` split. The unit test at `clients/typescript/test/client.unit.test.ts:18-21` pins the values so a future nats.js bump can't silently change behaviour.

## Test coverage

- `clients/typescript/test/client.unit.test.ts` — connect knobs, option construction.
- `clients/typescript/test/integration.test.ts` — full RPC round-trip against a real `nats-server` spawned in-process by vitest.

## Differences from the Go client

| Capability                              | Go client | TS client |
|-----------------------------------------|-----------|-----------|
| Inline `user`/`password` auth           | ✓         | ✓         |
| `creds_path` file-based auth            | ✓         | stub only |
| `runtime.json` URL override             | ✓         | ✗         |
| `Connect` / `connect` (file load)       | ✓         | ✓         |
| `ConnectWithConfig` / `connectWithConfig` | ✓       | ✓         |
| RPC, Publish, Subscribe, Query, KV*     | ✓         | ✓         |
| Typed verb constants                    | None — strings | None — strings |
| Reconnect defaults                      | Identical | Identical |

Both clients use string-typed verbs (not constants), so adding an RPC verb is a server-side change plus a regeneration of `types.generated.ts`; nothing on the TS side needs editing unless it wants typed wrappers.

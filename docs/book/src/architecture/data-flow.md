# Data flow

A single picture of where bytes travel between components.

## Hot path — live observation

```
agent sidecar  ──Publish──▶  NATS JetStream  ──Subscribe──▶  operator CLI / TUI
   (in container)                                              (on host)
```

Every agent frame, lifecycle transition, heartbeat, and audit event is published to NATS by the sidecar (or by `sextantd` for daemon-emitted events). Subscribers — the operator CLI's `conversation` and `tail` verbs, the TUIs, any third-party client — see the same envelopes in real time.

JetStream gives subscribers replay-from-sequence semantics: a TUI that comes online later can call `subscribe(subject, from_seq=N)` and catch up before going live. The Go client exposes this via `client.WithStartSeq(seq)` (`pkg/client/subscribe.go`); the TS client via `client.subscribeFromSeq(subject, fromSeq)` (`clients/typescript/src/client.ts:165`).

## Persistence path

```
agent sidecar  ──Publish──▶  NATS JetStream  ──Pull──▶  sextant-shipper  ──INSERT──▶  ClickHouse
```

`sextant-shipper` subscribes to every retained stream with a durable consumer and writes rows to ClickHouse. ClickHouse tables use `ReplacingMergeTree` keyed on envelope `id`, so at-least-once delivery is safe (`pkg/shipper`).

If ClickHouse is unreachable, the shipper spills to a local BoltDB buffer (`~/.local/share/sextant/shipper-buffer/buffer.db`) and drains in FIFO order when the database comes back. The shipper only ACKs the JetStream consumer after the row is durable (either in ClickHouse or in BoltDB). When the buffer hits its hard cap (10 GiB default), the shipper emits `audit.shipper_backpressure` and exits non-zero — fail-closed semantics.

## Query path

```
operator CLI  ──RPC──▶  sextantd  ──SELECT──▶  ClickHouse
```

History queries (`sextant audit list`, `sextant traces show <trace_id>`, `sextant agents show ...`) are RPCs on `sextant.rpc.<verb>`. The handler in `sextantd` runs a parametrised SELECT against ClickHouse and returns the rows. Catalog: `pkg/rpc/handlers/{query_history,query_audit,query_trace}.go`. Verb names: `query_history`, `query_audit`, `query_trace`. (The CLI verb `audit query` was renamed to `audit list` on 2026-05-27; the wire RPC verb keeps its `query_audit` name.)

## Control path — agent lifecycle

```
operator CLI ──RPC──▶ sextantd ──Docker SDK──▶ Docker daemon ──spawn──▶ container
                  └──Put KV──▶ NATS  (agent_definitions, agent_incarnations buckets)
                  └──Issue JWT──▶ pkg/authjwt
```

`sextant agents create`:

1. CLI calls `spawn_agent` RPC.
2. `pkg/rpc/handlers/spawn.go` looks up the template in NATS KV.
3. It creates the agent definition record (UUID, name, template ref) in the `agent_definitions` bucket and the incarnation record in `agent_incarnations`.
4. It issues a JWT via `pkg/authjwt` carrying the capability allowlist from `template.permissions`.
5. It calls `pkg/containermgr.Run()` with the right env vars, mounts, and labels.
6. The container starts; the sidecar in it connects to NATS, publishes `lifecycle.started`, and begins listening on `agents.<uuid>.inbox`.

## Tool path — agent calls sextant

```
agent SDK ──MCP/Streamable HTTP──▶ sextantd MCP server ──RPC or Publish──▶ NATS
            (Authorization: Bearer <JWT>)
```

The sidecar advertises the sextant MCP server to the Claude Agent SDK (`images/sidecar/entrypoint/src/index.ts:548-556`). When the SDK invokes a tool like `send_message`, the call travels over Streamable HTTP to `sextantd`'s MCP server (`http://host.docker.internal:5172/mcp`). The MCP server:

1. Extracts the JWT from `Authorization: Bearer <token>`.
2. Verifies it against the CA (`pkg/authjwt.CA.Verify`).
3. Checks the caller's `Capabilities` array contains the cap required for the tool (`pkg/mcpserver/tools.go:CapForTool`).
4. On success: dispatches the typed handler, publishes an `audit.tool_call` envelope, returns the result.
5. On capability denial: returns `capability_denied` error, audits with `result=denied`.

Stdio (Unix-socket) callers — the operator CLI and TUIs — bypass JWT verification; they inherit operator authority from Unix file permissions (`pkg/mcpserver/server.go` plus `specs/architecture.md` §10b).

## Inter-agent messages

```
agent A SDK ──MCP send_message──▶ sextantd MCP ──Publish──▶ agents.<B>.inbox ──Subscribe──▶ agent B sidecar
```

`send_message` doesn't connect agents directly. It always goes through `sextantd`, which audits the call and publishes the envelope on agent B's inbox subject. Agent B's sidecar is subscribed to its own inbox.

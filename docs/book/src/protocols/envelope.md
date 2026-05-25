# Envelope

Every message on the sextant bus — event, RPC request, RPC response, heartbeat — is wrapped in an `Envelope`. Source of truth: `pkg/sextantproto/envelope.go:17-34`. The JSON encoding is the wire format.

## Shape

```json
{
  "id": "11111111-1111-1111-1111-111111111111",
  "ts": "2026-05-25T15:30:00.000000Z",
  "proto_version": "1.0",

  "from": { "kind": "agent", "id": "aaaa…" },
  "to":   { "kind": "operator", "id": "operator" },

  "trace_id": "22222222-2222-2222-2222-222222222222",
  "span_id":  "33333333-3333-3333-3333-333333333333",
  "parent_span_id": null,

  "idempotency_key": null,
  "reply_to": null,

  "kind": "agent_frame",
  "payload": { … }
}
```

## Field-by-field

| Field             | Type           | Required? | Notes                                                                  |
|-------------------|----------------|-----------|------------------------------------------------------------------------|
| `id`              | UUID v4        | yes       | Unique per envelope. ClickHouse `events` table de-duplicates on `id`.  |
| `ts`              | RFC 3339 μs    | yes       | Custom `Timestamp.MarshalJSON` enforces 6 fractional digits.           |
| `proto_version`   | string         | yes       | `"1.0"` today. Additive bumps to minor; new namespaces for major.      |
| `from`            | `Address`      | yes       | Who emitted.                                                           |
| `to`              | `Address`      | no        | Absent for broadcast events.                                            |
| `trace_id`        | UUID           | yes       | Same for every envelope in a logical operation.                        |
| `span_id`         | UUID           | yes       | Unique per "step" inside the trace.                                    |
| `parent_span_id`  | UUID           | no        | Absent on root spans.                                                  |
| `idempotency_key` | string         | no        | RPC requests use this. Server caches `(verb, key)` ≈ 60s.              |
| `reply_to`        | string         | no        | NATS subject for the RPC reply.                                        |
| `kind`            | string         | yes       | Discriminator. See `Kind` list below.                                  |
| `payload`         | raw JSON       | yes       | Schema depends on `kind`.                                              |

## `Address`

`pkg/sextantproto/envelope.go:40-47`:

```json
{ "kind": "agent" | "operator" | "daemon" | "ui" | "external",
  "id":   "UUID or 'daemon-<host>' or 'operator'",
  "host": "host_id"  // optional
}
```

`AddressKind.IsValid()` enforces the enum.

## Kinds

The discriminator value picks the payload schema. Catalog (`pkg/sextantproto/envelope.go:69` + `AllKinds()`):

| `kind`                | Payload struct                  | Typical subject                                   |
|-----------------------|---------------------------------|---------------------------------------------------|
| `agent_frame`         | `AgentFramePayload`             | `agents.<uuid>.frames`                            |
| `lifecycle`           | `LifecyclePayload`              | `agents.<uuid>.lifecycle`                         |
| `audit`               | `AuditPayload`                  | `audit.<category>`                                |
| `telemetry_span`      | `Span`                          | `telemetry.traces.<host>`                         |
| `telemetry_metric`    | `Metric`                        | `telemetry.metrics.<host>`                        |
| `telemetry_log`       | `LogRecord`                     | `telemetry.logs.<host>`                           |
| `user_input_request`  | `UserInputRequestPayload`       | `user_input.requests.<from_uuid>`                 |
| `user_input_response` | `UserInputResponsePayload`      | `user_input.responses.<request_id>`               |
| `rpc_request`         | `RPCRequest`                    | `sextant.rpc.<verb>`                              |
| `rpc_response`        | `RPCResponse`                   | (the requester's `reply_to`)                      |
| `heartbeat`           | `HeartbeatPayload`              | `agents.<uuid>.heartbeat`                         |

Per-kind payload shapes live in `pkg/sextantproto/payloads.go` and `pkg/sextantproto/telemetry.go`.

## Validation

`Envelope.Validate()` (`pkg/sextantproto/envelope.go:154-177`) asserts:

- `id` is non-zero.
- `trace_id` and `span_id` are non-zero.
- `proto_version` is non-empty.
- `kind` is non-empty.
- `from.kind` is one of the `AddressKind` enum values.

The RPC server runs `Validate` on every inbound message. Subscribers (`pkg/client.Subscribe`, the shipper) don't validate by default — they trust the writer.

## Versioning

- `proto_version` is `MAJOR.MINOR`. No PATCH; the wire format is the contract.
- **Minor bumps are additive only** — new fields, new envelope kinds, new payload variants. Old consumers ignore unknown fields (Go's `json.Unmarshal` does that by default; TS `json-schema-to-typescript` produces compatible interfaces).
- **Major bumps are breaking** — new subject namespace (e.g. `agents.v2.*`), parallel publishing during the transition window.

The current version is `1.0`. No major bump has happened. There's no automated migration tooling — when a major bump comes, it's a manual, codified migration.

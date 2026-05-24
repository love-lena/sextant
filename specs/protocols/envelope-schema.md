# Envelope schema — protocol spec

Every message on the bus (event or RPC) is an `Envelope`. The envelope is JSON-encoded for debuggability.

## Envelope structure

```go
type Envelope struct {
    // Identity
    ID           uuid.UUID `json:"id"`              // unique per envelope
    Ts           Timestamp `json:"ts"`              // when emitted (μs precision)
    ProtoVersion string    `json:"proto_version"`   // semver of the envelope protocol; e.g. "1.0"

    // Addressing
    From         Address   `json:"from"`            // who emitted
    To           *Address  `json:"to,omitempty"`    // optional target; absent for broadcast events

    // Tracing
    TraceID      *uuid.UUID `json:"trace_id,omitempty"`
    SpanID       *uuid.UUID `json:"span_id,omitempty"`
    ParentSpanID *uuid.UUID `json:"parent_span_id,omitempty"`

    // Idempotency (for RPC)
    IdempotencyKey *string `json:"idempotency_key,omitempty"`
    ReplyTo        *string `json:"reply_to,omitempty"`  // NATS subject for reply

    // Payload
    Kind    string          `json:"kind"`        // discriminator, e.g. "agent_frame", "rpc_request"
    Payload json.RawMessage `json:"payload"`     // schema depends on Kind
}
```

## Address

```go
type Address struct {
    Kind string  `json:"kind"`  // "agent" | "operator" | "daemon" | "ui" | "external"
    ID   string  `json:"id"`    // UUID for agent/operator/ui; "daemon-<host>" for daemon
    Host *string `json:"host,omitempty"`  // host_id for multi-host scoping
}
```

## Versioning rules

- `proto_version` follows semver-ish: `MAJOR.MINOR`. No PATCH because the wire format is the contract.
- **Minor bumps are additive only** — new fields, new envelope kinds, new payload variants. Old consumers ignore unknown fields.
- **Major bumps are breaking** — new subject namespace (`agents.v2.*` etc.), parallel publishing during transition windows.

## Kinds (initial set)

Listed in shorthand; each has its own payload schema defined alongside.

| Kind | Payload | Subject pattern (typical) |
|---|---|---|
| `agent_frame` | tool calls, assistant text, etc. | `agents.<uuid>.frames` |
| `lifecycle` | started, ended, paused, archived | `agents.<uuid>.lifecycle` |
| `audit` | action, actor, capability, result | `audit.<category>` |
| `telemetry_span` | OTel-shaped span | `telemetry.traces.<host>` |
| `telemetry_metric` | OTel-shaped metric | `telemetry.metrics.<host>` |
| `telemetry_log` | OTel-shaped log record | `telemetry.logs.<host>` |
| `user_input_request` | question, options, urgency | `user_input.requests.<from_uuid>` |
| `user_input_response` | answer | `user_input.responses.<request_id>` |
| `rpc_request` | verb, args | `sextant.rpc.<verb>` |
| `rpc_response` | result, error | (reply subject) |
| `heartbeat` | health snapshot | `agents.<uuid>.heartbeat` |
| `kv_update` | bucket, key, value, op | (NATS KV-native, not wrapped in Envelope) |

## JSON Schema generation

Go is source of truth. `pkg/sextantproto/` defines all types; `go generate` produces JSON Schemas committed to `pkg/sextantproto/schemas/*.json`.

TS client consumes these schemas via `json-schema-to-typescript` in its build.

## Open

- Should `Address` accept a "wildcard" form for fan-out subjects, or always be a concrete reference?
- Compact encoding for hot paths — JSON is debuggable but heavy. Optional msgpack later? Lean: no, JSON only for initial. Optimize if measured to matter.
- Backward-compat policy duration — how long do we dual-publish on major bumps? Probably 2 minor versions.

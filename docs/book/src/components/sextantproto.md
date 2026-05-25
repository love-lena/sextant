# sextantproto

**Source**: `pkg/sextantproto/`, `cmd/sextantproto-gen/`.

The single source of truth for sextant's wire types. Every envelope on the bus, every RPC request/response, and every payload kind has a Go struct here. JSON schemas are generated from these structs and committed to the repo so the TypeScript client can stay in lock-step without re-typing anything.

## When to reach for this component

- You're adding a new envelope kind, RPC verb, or payload field.
- You need to know the exact JSON shape on the wire.
- You're updating the TypeScript types and need to regenerate.

## Exported type families

| Family       | File                                  | Highlights                                                       |
|--------------|---------------------------------------|------------------------------------------------------------------|
| Envelope     | `pkg/sextantproto/envelope.go:17`     | `Envelope`, `Address`, `AddressKind`, `Kind`, `Timestamp`.       |
| Identity     | `pkg/sextantproto/agent.go`           | `AgentDefinition`, `AgentIncarnation`, `LifecycleState`, `IncarnationState`, `RuntimeConfig`, `SandboxConfig`, `ResourceLimits`. |
| Payloads     | `pkg/sextantproto/payloads.go`        | `AgentFramePayload`, `LifecyclePayload`, `AuditPayload`, `UserInputRequestPayload`, `UserInputResponsePayload`, `HeartbeatPayload`. |
| OTel         | `pkg/sextantproto/telemetry.go:15`    | `Span`, `Metric`, `LogRecord`, `SpanKind`, `StatusCode`, `SpanEvent`, `SpanLink`. |
| RPC core     | `pkg/sextantproto/rpc.go`             | `RPCRequest`, `RPCResponse`, `RPCError`, error codes.            |
| RPC verbs    | `pkg/sextantproto/rpcverbs.go`        | Request/response structs for each implemented verb.              |
| Worktree     | `pkg/sextantproto/rpcverbs.go:310+`   | `WorktreeStatus`, `WorktreeInfo`, plus Worktree*Request/Response. |

## Envelope structure

```go
type Envelope struct {
    ID             uuid.UUID    // unique per envelope
    Ts             Timestamp    // when emitted (μs precision)
    ProtoVersion   string       // "1.0"
    From           Address      // who emitted
    To             *Address     // optional target

    TraceID        uuid.UUID    // required on every envelope
    SpanID         uuid.UUID    // required
    ParentSpanID   *uuid.UUID   // optional; absent on roots

    IdempotencyKey *string      // RPC requests
    ReplyTo        *string      // RPC reply subject

    Kind           string       // discriminator
    Payload        json.RawMessage
}
```

(See `pkg/sextantproto/envelope.go:17-34` and [Envelope protocol](../protocols/envelope.md) for the full description.)

`Timestamp` is RFC 3339 with 6-digit microsecond precision (`pkg/sextantproto/envelope.go:182`); custom `MarshalJSON`/`UnmarshalJSON` enforce that format.

## Validation

`Envelope.Validate()` (`pkg/sextantproto/envelope.go:154-177`) asserts: `id` non-zero, `trace_id` non-zero, `span_id` non-zero, `proto_version` set, `kind` set, `from.kind` valid. The RPC server calls this on every inbound request.

## Kinds

`Kind` (`pkg/sextantproto/envelope.go:69`) is a string enum. `AllKinds()` returns the canonical list:

```
agent_frame
lifecycle
audit
telemetry_span
telemetry_metric
telemetry_log
user_input_request
user_input_response
rpc_request
rpc_response
heartbeat
```

Each kind corresponds to a typed payload struct in `payloads.go` or `telemetry.go`.

## JSON schema generation

`cmd/sextantproto-gen/main.go` is invoked by `go generate ./pkg/sextantproto/...`. It:

1. Uses `github.com/invopop/jsonschema` to reflect every exported type.
2. Writes one JSON Schema file per type to `pkg/sextantproto/schemas/<type>.json`.
3. Canonicalises (sorted keys, one trailing newline) and uses `writeIfChanged` to preserve mtimes when re-running on no-op input.

The TS client's `npm run codegen` reads these schemas and emits `clients/typescript/src/types.generated.ts` via `json-schema-to-typescript` (`clients/typescript/scripts/codegen.ts`). Anything in `schemas/` that isn't yet wired through codegen is harmless.

## Wire convention

Sextant keeps **JSON on the wire** for debuggability. Binary encodings are out of scope. New fields are additive only; breaking changes get a new namespace and a transition window (see the [Envelope protocol](../protocols/envelope.md) chapter).

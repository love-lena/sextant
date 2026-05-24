# Client libraries — component spec

Two libraries, mirroring each other in API. Both built from the same JSON Schema source of truth generated from Go structs in M1.

## `sextant-client-go` (Go)

Package: `github.com/love-lena/sextant-initial/pkg/client`

### API surface

`Envelope` is `github.com/love-lena/sextant-initial/pkg/sextantproto.Envelope`. `KVUpdate`, `QueryFilter`, and the option types live in `pkg/client`.

```go
// Connect loads ~/.config/sextant/client.toml and dials the configured NATS.
// ConnectWithConfig takes an already-parsed Config instead.
func Connect(ctx context.Context, configPath string, opts ...Option) (*Client, error)
func ConnectWithConfig(ctx context.Context, cfg Config, opts ...Option) (*Client, error)

// Close releases the underlying NATS connection. Idempotent.
func (c *Client) Close() error

// Message wraps a received envelope with JetStream metadata.
type Message struct {
    Envelope    sextantproto.Envelope
    Subject     string
    StreamSeq   uint64    // JetStream stream sequence (use for resume)
    ConsumerSeq uint64    // JetStream consumer sequence
    Timestamp   time.Time // JetStream-reported receive ts
    // Ack acknowledges the message to JetStream. Safe to call once.
    Ack func() error
}

// Subscribe to a subject pattern. Default delivery is "new" — messages
// published after Subscribe returns. Override with SubscribeOption.
func (c *Client) Subscribe(ctx context.Context, subject string, opts ...SubscribeOption) (<-chan Message, error)

// SubscribeFromSeq does gap-fill replay from a stream sequence then
// transitions to live. Equivalent to Subscribe(subject, WithStartSeq(fromSeq)).
func (c *Client) SubscribeFromSeq(ctx context.Context, subject string, fromSeq uint64) (<-chan Message, error)

// Publish an envelope. (M7.)
func (c *Client) Publish(ctx context.Context, subject string, env sextantproto.Envelope) error

// RPC calls a sextant verb with typed request/reply. (M7.)
func (c *Client) RPC(ctx context.Context, verb string, req any, resp any, opts ...RPCOption) error

// Query past events from ClickHouse via the query_history RPC. (M7.)
// In M4 this returns a sentinel ErrNotImplementedYet referencing the M7
// milestone — it must NOT silently return an empty slice.
func (c *Client) Query(ctx context.Context, filter QueryFilter) ([]sextantproto.Envelope, error)

// WatchKV subscribes to changes on a KV key. The channel emits one
// KVUpdate per change; on the initial subscription, current values are
// emitted before live updates begin.
func (c *Client) WatchKV(ctx context.Context, bucket, key string) (<-chan KVUpdate, error)

// GetKV reads current value once. Returns ErrKVKeyNotFound when the key
// is absent (not a nil byte slice with nil error).
func (c *Client) GetKV(ctx context.Context, bucket, key string) ([]byte, error)

// PutKV writes a value. (M7.)
func (c *Client) PutKV(ctx context.Context, bucket, key string, value []byte) error

// KVUpdate describes one change to a KV key.
type KVUpdate struct {
    Bucket    string
    Key       string
    Value     []byte    // empty on Delete / Purge
    Revision  uint64
    Op        KVOp      // Put | Delete | Purge
    Timestamp time.Time
}

type KVOp string
const (
    KVOpPut    KVOp = "put"
    KVOpDelete KVOp = "delete"
    KVOpPurge  KVOp = "purge"
)
```

### Options (functional options pattern)

```go
WithIdempotencyKey(key string)  // for RPC
WithTimeout(d time.Duration)
WithCapability(cap string)      // future: present JWT with cap
WithTracer(t Tracer)            // OTel integration
```

### Config file

The library reads `~/.config/sextant/client.toml`. Schema (TOML):

```toml
# Mandatory.
[nats]
url = "nats://127.0.0.1:4222"   # full NATS URL; loopback for initial.

# Operator credentials. Exactly one of password / creds_path must be set.
# `creds_path` is the production path; `password` is convenient for tests
# and ad-hoc development. Both forms hit the loopback TCP listener; the
# Unix-file-perm boundary applies to whichever file holds the secret.
[operator]
user        = "operator"
password    = ""                              # inline (mode-0600 file required if used)
creds_path  = "~/.config/sextant/operator.creds"   # NATS creds file written by `sextant init`

# Optional. Defaults filled by LoadConfig.
[client]
connect_timeout = "10s"      # cap on initial dial
request_timeout = "30s"      # default for RPC / Query
log_level       = "info"     # trace | debug | info | warn | error
```

`LoadConfig(path string) (Config, error)` parses this file. `~/` is expanded
against `os.UserHomeDir()`. Missing optional fields take the defaults above.

Until M5 writes `operator.creds`, the M4 library treats inline `password`
as the supported configuration; `creds_path` is accepted but the M4 NATS
binding is currently password-based (per `specs/components/nats.md` §"Config").

### Auth

Initial: the library connects to the loopback TCP listener as the operator
user. The trust boundary is Unix file perms on whichever file carries the
secret (`client.toml` if `password` is inline, or `operator.creds` once M5
ships and `creds_path` is in use). NATS Server has no native Unix-socket
transport — see `specs/components/nats.md` §"Config".

When 10b multi-user lands: library reads operator JWT from config and presents it on connect.

For agent-side (sidecar) use, JWT comes from env var `SEXTANT_JWT`.

## `@sextant/client` (TypeScript)

Package: `@sextant/client` (npm)

### API surface (mirrors Go)

```typescript
async function connect(opts?: ConnectOptions): Promise<Client>;

interface Message {
    envelope: Envelope;
    subject: string;
    streamSeq: bigint;     // JetStream stream sequence (use for resume)
    consumerSeq: bigint;   // JetStream consumer sequence
    timestamp: Date;
    ack(): Promise<void>;
}

class Client {
    subscribe(subject: string, opts?: SubscribeOptions): AsyncIterable<Message>;
    subscribeFromSeq(subject: string, fromSeq: bigint): AsyncIterable<Message>;
    publish(subject: string, env: Envelope): Promise<void>;
    rpc<Req, Resp>(verb: string, req: Req, opts?: RPCOptions): Promise<Resp>;
    query(filter: QueryFilter): Promise<Envelope[]>;
    watchKV(bucket: string, key: string): AsyncIterable<KVUpdate>;
    getKV(bucket: string, key: string): Promise<Uint8Array | null>;
    putKV(bucket: string, key: string, value: Uint8Array): Promise<void>;
}
```

### Types

Generated from JSON Schemas (produced by M1) via `json-schema-to-typescript`. Build step in CI checks types are up to date.

Verb payload structs (`ListAgentsRequest`, `ListAgentsResponse`,
`GetAgentStatusRequest`, `GetAgentStatusResponse`, `ReadFileRequest`,
`ReadFileResponse`, `QueryHistoryRequest`, `QueryHistoryResponse`, plus
their nested filter / time-range / summary types) live in
`pkg/sextantproto/rpcverbs.go`. The Go RPC dispatch metadata (verb name
constants, `CapFor`) stays in `pkg/rpc/types.go`. JSON Schemas for the
verb payloads are emitted under `pkg/sextantproto/schemas/` by
`go generate` and consumed by both the Go handlers and the
`@sextant/client` TypeScript codegen — single source of truth for the
wire shape.

## Shared concerns

- **Reconnection**: built-in with exponential backoff; loss of connection emits an event on a special control channel; client subscriptions auto-resume from the `StreamSeq` of the last-acked `Message`. Initial knobs (Go): `nats.MaxReconnects(-1)`, `nats.ReconnectWait(500ms)`, `nats.ReconnectJitter(100ms, 500ms)`. Subscribe uses a JetStream ordered consumer so reset/resume is handled by the server.
- **Timeouts**: every RPC has a default timeout (10s); override via option
- **Idempotency**: every RPC carries a client-generated idempotency key (UUID); server dedupes within a bounded window (60s)
- **Type validation**: every received envelope's payload is type-checked against its declared kind; type mismatch → returned as an error to the caller, not silently coerced

## Milestone scoping (Go)

| Milestone | Methods landed | Notes |
|---|---|---|
| M4 | `Connect`, `ConnectWithConfig`, `Close`, `Subscribe`, `SubscribeFromSeq`, `WatchKV`, `GetKV`, `LoadConfig` | Read path only. `Query` is exported but returns `ErrNotImplementedYet` referencing M7. `Publish`, `RPC`, `PutKV` are not exported yet. |
| M7 | `Publish`, `RPC`, `PutKV`; `Query` switches to real ClickHouse RPC | Write path + RPC. `query_history` RPC is the first real verb that backs `Query`. |

## Open

- Streaming RPC patterns — multi-message reply on the same reply subject vs ephemeral subject for the stream. Lean: ephemeral subject; cleaner cancellation
- Connection multiplexing — one client = one NATS connection? Or pool? Lean: one client = one connection (NATS handles internal multiplexing)
- Subject ACL enforcement — done at server side. Client just gets errors back; doesn't pre-validate.

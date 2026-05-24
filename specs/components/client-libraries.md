# Client libraries — component spec

Two libraries, mirroring each other in API. Both built from the same JSON Schema source of truth generated from Go structs in M1.

## `sextant-client-go` (Go)

Package: `github.com/<org>/sextant-initial/pkg/client`

### API surface

```go
// Connect loads ~/.config/sextant/client.toml and dials the configured NATS.
func Connect(ctx context.Context, opts ...Option) (*Client, error)

// Subscribe to a subject pattern.
func (c *Client) Subscribe(ctx context.Context, subject string, opts ...SubscribeOption) (<-chan Envelope, error)

// SubscribeFromSeq does gap-fill replay from a sequence then transitions to live.
func (c *Client) SubscribeFromSeq(ctx context.Context, subject string, fromSeq uint64) (<-chan Envelope, error)

// Publish an envelope.
func (c *Client) Publish(ctx context.Context, subject string, env Envelope) error

// RPC calls a sextant verb with typed request/reply.
func (c *Client) RPC(ctx context.Context, verb string, req any, resp any, opts ...RPCOption) error

// Query past events from ClickHouse via the query_history RPC.
func (c *Client) Query(ctx context.Context, filter QueryFilter) ([]Envelope, error)

// WatchKV subscribes to changes on a KV key.
func (c *Client) WatchKV(ctx context.Context, bucket, key string) (<-chan KVUpdate, error)

// GetKV reads current value once.
func (c *Client) GetKV(ctx context.Context, bucket, key string) ([]byte, error)

// PutKV writes a value.
func (c *Client) PutKV(ctx context.Context, bucket, key string, value []byte) error
```

### Options (functional options pattern)

```go
WithIdempotencyKey(key string)  // for RPC
WithTimeout(d time.Duration)
WithCapability(cap string)      // future: present JWT with cap
WithTracer(t Tracer)            // OTel integration
```

### Auth

Initial: just connects via Unix socket; file perms gate access. The library reads the local NATS socket path from config; no token presented.

When 10b multi-user lands: library reads operator JWT from config and presents it on connect.

For agent-side (sidecar) use, JWT comes from env var `SEXTANT_JWT`.

## `@sextant/client` (TypeScript)

Package: `@sextant/client` (npm)

### API surface (mirrors Go)

```typescript
async function connect(opts?: ConnectOptions): Promise<Client>;

class Client {
    subscribe(subject: string, opts?: SubscribeOptions): AsyncIterable<Envelope>;
    subscribeFromSeq(subject: string, fromSeq: bigint): AsyncIterable<Envelope>;
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

## Shared concerns

- **Reconnection**: built-in with exponential backoff; loss of connection emits an event on a special control channel; client subscriptions auto-resume from the last seen seq
- **Timeouts**: every RPC has a default timeout (10s); override via option
- **Idempotency**: every RPC carries a client-generated idempotency key (UUID); server dedupes within a bounded window (60s)
- **Type validation**: every received envelope's payload is type-checked against its declared kind; type mismatch → returned as an error to the caller, not silently coerced

## Open

- Streaming RPC patterns — multi-message reply on the same reply subject vs ephemeral subject for the stream. Lean: ephemeral subject; cleaner cancellation
- Connection multiplexing — one client = one NATS connection? Or pool? Lean: one client = one connection (NATS handles internal multiplexing)
- Subject ACL enforcement — done at server side. Client just gets errors back; doesn't pre-validate.

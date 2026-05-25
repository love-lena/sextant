# Go client (`pkg/client`)

**Source**: `pkg/client/`.

The Go client is used by the `sextant` CLI, by every Go TUI, and by anything else in this repo that talks to sextant. It is the canonical reference implementation.

## When to reach for this component

- You're writing a Go program that needs to read from or write to the bus.
- You're building a new TUI on top of `pkg/client`.
- You're investigating connect/reconnect behaviour or runtime-file discovery.

## Public surface

| Symbol                                                     | File                          | Purpose                                                  |
|------------------------------------------------------------|-------------------------------|----------------------------------------------------------|
| `Connect(ctx, configPath, opts...)`                        | `pkg/client/client.go:103`    | Load config from disk, dial NATS, return `*Client`.      |
| `ConnectWithConfig(ctx, cfg, opts...)`                     | `pkg/client/client.go:128`    | Same, but skip the file load.                            |
| `Client.Close()`                                           | `pkg/client/client.go:193`    | Idempotent disconnect.                                   |
| `Client.Config()`                                          | `pkg/client/client.go:229`    | Effective config (post `runtime.json` override).         |
| `Client.RPC(ctx, verb, args, resp, opts...)`               | `pkg/client/rpc.go:92`        | Typed request/reply.                                     |
| `Client.Publish(ctx, subject, env)`                        | `pkg/client/publish.go:18`    | Publish a sextantproto envelope.                         |
| `Client.Subscribe(ctx, subject, opts...)`                  | `pkg/client/subscribe.go`     | Returns `<-chan Message`. JetStream-backed.              |
| `Client.Query(ctx, filter)`                                | `pkg/client/query.go:35`      | History via `query_history` RPC.                         |
| `Client.PutKV(ctx, bucket, key, value)`                    | `pkg/client/kv.go:35`         | KV put.                                                  |
| `Client.GetKV(ctx, bucket, key)`                           | `pkg/client/kv.go:54`         | KV get.                                                  |
| `Client.WatchKV(ctx, bucket, key)`                         | `pkg/client/kv.go:88`         | KV change stream.                                        |
| `LoadConfig(path)`                                         | `pkg/client/config.go:143`    | Parse `client.toml`; expand `~/`.                        |
| `DefaultConfigPath()` / `DefaultRuntimePath()`             | `pkg/client/config.go:89, 105`| Canonical paths.                                         |
| `WithTimeout` / `WithIdempotencyKey` / `WithDeliverAll` / `WithStartSeq` | `pkg/client/{rpc,subscribe}.go` | Option helpers.                  |

## Configuration

`~/.config/sextant/client.toml`:

```toml
[nats]
url = "nats://127.0.0.1:4222"

[operator]
user        = "operator"                          # default "operator"
password    = ""                                  # use either this...
creds_path  = "~/.config/sextant/operator.creds"  # ...or this

[client]
connect_timeout = "10s"
request_timeout = "30s"
log_level       = "info"
```

The struct is `pkg/client.Config` (`pkg/client/config.go:20`). All fields are populated with defaults by `LoadConfig` if missing.

> **`runtime.json` override**: if `~/.local/share/sextant/runtime.json` exists, the client reads its `nats_addr` and uses that *instead of* `client.toml`'s URL (`pkg/client/client.go:119`). This is how kernel-picked ports work: clients don't need to be reconfigured when the daemon rebinds.

## RPC ergonomics

`Client.RPC` takes any `args` and decodes into any `resp`. The client wraps the call in an envelope with a fresh trace ID and a default 10-second timeout.

```go
var resp sextantproto.ListAgentsResponse
err := c.RPC(ctx, "list_agents", sextantproto.ListAgentsRequest{}, &resp)
```

Options:

- `WithTimeout(d)` — override the request timeout for this call.
- `WithIdempotencyKey(key)` — the server caches replies for ~60 seconds keyed on `(verb, idempotency_key)`.

`RPCError` is the typed shape returned by the server (`pkg/client/rpc.go:29`); `ErrRPCTimeout` is the sentinel for client-side timeout (`:43`).

## Subscribe ergonomics

```go
msgs, err := c.Subscribe(ctx, "agents.*.frames", client.WithDeliverAll())
for m := range msgs {
    if m.Err != nil { /* … */ continue }
    // m.Envelope, m.Subject, m.StreamSeq, m.ConsumerSeq, m.Timestamp
    _ = m.Ack()
}
```

`Subscribe` binds a JetStream ephemeral consumer to whatever stream contains the subject. `WithDeliverAll` replays from the beginning of the stream; `WithStartSeq(N)` resumes from a specific sequence. `cmd/sextant-client-demo/main.go` is a working example.

## Reconnection

The client passes these NATS options on Connect (`pkg/client/client.go:145-147`):

```
MaxReconnects(-1)        // unlimited
ReconnectWait(500ms)
ReconnectJitter(100ms, 500ms)
```

Caller code doesn't need to handle reconnection; `Subscribe`/`Publish`/`RPC` block briefly and resume.

## Auth

The Go client supports two operator-credential paths (`pkg/client/config.go`):

- Inline: `[operator] user = "operator"  password = "..."`. The client passes these to NATS as a user/password connection.
- Creds file: `creds_path = "~/.config/sextant/<file>.creds"` — a NATS credentials file (the `-----BEGIN NATS USER JWT-----` format produced by `nsc` or equivalent). Passed straight to `nats.UserCredentials(path)` (`pkg/client/client.go:151`).

Sextant's bootstrap currently writes inline operator user/password into `operator.creds` as TOML (sextantd parses it via `pkg/sextantd.ReadOperatorCreds`), so the *operator* path uses inline auth at this snapshot. The `creds_path` field exists for future per-NATS-user JWTs but the operator path doesn't use it today.

Unix file permissions on `operator.creds` are the trust boundary (`specs/architecture.md` §10b).

## Test coverage

- `pkg/client/client_test.go` — lifecycle, ctx cancel.
- `pkg/client/config_test.go` — TOML parsing.
- `pkg/client/rpc_test.go` — dispatch, timeouts, idempotency.
- `pkg/client/runtime_override_test.go` — `runtime.json` precedence.

## Example: a minimal subscriber

```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer cancel()

c, err := client.Connect(ctx, "")  // "" uses DefaultConfigPath
if err != nil { return err }
defer c.Close()

msgs, err := c.Subscribe(ctx, "agents.*.lifecycle")
if err != nil { return err }

for m := range msgs {
    fmt.Printf("[%s] %s\n", m.Subject, m.Envelope.Kind)
    _ = m.Ack()
}
return nil
```

This is essentially what `sextant tail` does.

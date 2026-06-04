# NATS binding

How the NATS backend realises Sextant's verbs and contract. This is the
substrate-specific layer (ADR-0013): replacing NATS rewrites *this file* and the
SDK's realisation, leaving `lexicons/`, `methods.json`, and
`semantic-contract.md` untouched. `pkg/wire` + `pkg/sx` are the Go expression of
what's written here; a non-Go NATS client implements against this doc.

NATS provides three subsystems we use: **JetStream stream** (the durable message
log), **JetStream KV** (artifacts + registry + meta), and **core pub/sub** (the
drain broadcast only).

## Namespace + topology (provisioned by the operator at bootstrap)

- **Stream `MESSAGES`** captures subjects under `msg.>` (the user messages
  space). Named topics are `msg.topic.<name>`; direct addressing is
  `msg.agent.<id>` — conventions, not constructs.
- **KV buckets**: `ARTIFACTS` (client-writable, 64 revisions), `sx_clients`
  (registry), `sx_meta` (public protocol metadata; key `epoch`), `sx_workflows`
  (reserved).
- **Reserved subjects**: `sx.control.*` (operator-only; the bus denies client
  writes), `sx.workflow.*` (workflow convention). `sx.control.drain` is the
  cooperative-drain broadcast.
- **Guardrail**: clients may not write `sx.control.*` or perform stream/bucket
  lifecycle ops. Everything under `msg.>` and writes to `ARTIFACTS`/`sx_clients`
  are open (conventions enforce the rest).

## Auth + identity

Decentralized JWT: one operator key, one `SEXTANT` account, **one user JWT per
client** (minted by `sextant token <id>`). A client connects with its
credentials file (`nats.UserCredentials`); the **identity is the user-name
claim** in that JWT — unforgeable (editing the name breaks the signature), so
`sender` and the registry key are exactly what the bus authenticated. (ADR-0012,
ADR-0015.)

## Connect handshake

1. Resolve the bus URL (explicit, or the `url` field of the `bus.json` discovery
   file).
2. Read the client id = the user-name claim of the credentials JWT.
3. `nats.Connect(url, UserCredentials(creds), Name(id), MaxReconnects(-1))` — a
   dropped connection reconnects forever; only an explicit drain ends a client.
4. **Epoch gate**: KV `sx_meta` → `Get("epoch")` → integer; refuse to proceed
   unless it exactly equals the client's protocol epoch.
5. **Register**: KV `sx_clients` → `Put(id, <client record JSON>)` (the bare
   `client` lexicon: `{id, kind, epoch, sdk, connected_at}`).
6. **Drain watch**: core `Subscribe("sx.control.drain", …)`, then flush so the
   subscription is server-side registered before connect returns.

**Close**: KV `sx_clients` → `Delete(id)` (best-effort registry leave), then
close the connection.

## Per-verb binding

| Verb | NATS operation |
|---|---|
| `message.publish` | `js.Publish(subject, encode(envelope))` where `subject` is under `msg.`; the bytes are the `envelope` lexicon wrapping the record. Returns the stream `PubAck` (sequence). |
| `message.read` | An **ephemeral** consumer on `MESSAGES`, `FilterSubjects=[subject]`, start at `since` (`OptStartSeq`; `0` → `DeliverAll`), `Fetch(limit)`. `next_cursor` = last delivered stream sequence + 1. |
| `message.subscribe` | An **ordered** consumer on `MESSAGES`, `FilterSubjects=[subject]`, `DeliverPolicy` = New (default) or All (`deliver: all`). For each message: decode + `Validate` envelope, `CheckEpoch`, `CheckSkew` against the bus-stamped time; **quarantine** (skip + log) on any failure, else deliver. Ephemeral — no per-subscriber state on the bus. |
| `clients.list` | KV `sx_clients` → `Keys()` (ignore deletes; empty bucket → empty list, not an error) → per-key `Get` → decode the `client` lexicon. The **key is the authoritative id**; reject a record whose body id disagrees with its key. |
| `artifact.create` | KV `ARTIFACTS` → `Create(name, record)` (the **bare** lexicon, no envelope); fails if the name exists. Returns the revision. |
| `artifact.update` | KV `ARTIFACTS` → `Update(name, record, expected_rev)` — compare-and-set; fails if the current revision moved. Returns the new revision. |
| `artifact.get` | KV `ARTIFACTS` → `Get(name)` → value (bare lexicon), revision, created time. |
| `artifact.delete` | KV `ARTIFACTS` → `Delete(name)`. |
| `artifact.watch` | KV `ARTIFACTS` → `Watch(name)` → current value first, then each change. `Operation != Put` → a delete. A `nil` entry marks the end of the initial replay. |

## What's enveloped vs bare

- **Enveloped** (the `envelope` lexicon wraps the record): `message.publish`,
  `message.read`, `message.subscribe`.
- **Bare** (the lexicon stored/returned directly, no envelope): all `artifact.*`
  and the `clients.list` record.

A client that gets this wrong — wrapping artifacts, or publishing a bare record
to `msg.>` — is not rejected by the bus; its messages are simply quarantined by
every receiver. Getting it right is the binding's job.

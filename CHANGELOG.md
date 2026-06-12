# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **The principal claims itself on first enrollment** (ADR-0031) — a self-enrolling
  human seat claims the still-unclaimed principal as part of `sextant clients
  register --self`, so first-run needs no separate `principal set`; `--no-principal`
  opts out. The bus enforces the human-only guarantee at the source: only a
  non-agent seat may be claimed, so an auto-minting agent can never claim the
  principal even though it shares the enrollment path. The enrollment credential
  gains `principal.set`, bus-gated to claim-when-unclaimed only.
- **`register --self` creates an active context** (ADR-0021) — self-enrollment now
  writes the new creds into the context store (`$SEXTANT_HOME`, not the bus
  `--store`), records a context carrying the bus-minted ULID, and makes it active,
  so the very next `publish`/`subscribe`/… runs with no connection flags. Held-mode
  `register <name>` (minting for someone else) is unchanged — it writes a creds
  file to hand off and creates no context. The M2 acceptance e2e is now hermetic (a
  per-run `$SEXTANT_HOME`) so the issuance transcript stays deterministic.
- **Saved client contexts** (ADR-0021) — `sextant context add|use|list|current|delete`.
  A *context* is a local (bus URL + identity + creds) profile under a name you
  choose, so `publish`/`subscribe`/`artifact`/… need no `--creds`/`--url` once one
  is active — the `kubectl`/`nats context` pattern. Connection resolution is a
  precedence chain: explicit `--creds`/`$SEXTANT_CREDS` (URL from `--url`/`--store`
  discovery) → a context named by `--context`/`$SEXTANT_CONTEXT` → the active
  context. Contexts live under `$SEXTANT_HOME` (default `<user-config>/sextant`),
  separate from the bus `--store`; the credential is kept in its own `0600` file
  and referenced by path. Context commands are local-administration (like `up`),
  not protocol operations, so they stay out of `methods.json`.
- `cmd/sextant`: **environment defaults for the connection flags** — `$SEXTANT_STORE`
  backs `--store` and `$SEXTANT_CREDS` backs `--creds`, so a shell that exports them
  once need not repeat the flags on every command. Precedence is explicit flag >
  env var > built-in default (a per-user config path for the store; required-error
  for creds). Matches the existing `$SEXTANT_SELF_NAME` convention.
- **Clients are bus-issued identities** (ADR-0020) — the identity half of M2.
  - **One issuance path, two auth modes.** `clients.register` now *mints a new
    identity* (a ULID + its credential) and persists a durable record, returning
    the creds; the signing keys never leave the bus (key custody). It is the single
    exception to "you must already be someone," authorized by the caller's
    authority: a held identity (the **operator**, minting for another) or a
    bootstrap/enrollment authorization with no pre-existing identity (the
    **enrollment** connection tier — locality-trusted, minting for self). `sextant
    up` provisions both reserved credentials in the store; the CLI never touches the
    signing keys. `sextant token` is gone — there is no offline mint.
  - **Connection-derived presence.** The bus computes online/offline first-hand
    from its own connection table (`Connz` AuthorizedUser ≡ the record's
    authenticated subject), not from a register/deregister call. No heartbeat, no
    ghost-reaping; a disconnected client is legitimately offline, not stale.
  - **Durable identity store.** The clients registry is a persisted directory of
    issued identities that survives disconnect and bus restart. `clients.list` is
    the join of records with live presence — it shows offline clients by default,
    each with an `online`/`offline` column.
  - **Three lifecycle events, not two.** *Register* mints (once); *disconnect*
    drops presence to offline (a clean SDK `Close` no longer deregisters); *retire*
    (`clients.retire`, operator-only) decommissions for good — removes the record
    and drops any live connection. The connect handshake is now `clients.hello`
    (confirms a known identity + folds the epoch hard-gate), which also makes retire
    effective: a retired identity can no longer complete a handshake.
  - **SDK:** new `Issuer` (`ConnectIssuer` + `Register`/`Enroll`/`Retire`) for the
    issuance/decommission path; `ClientInfo` gains `Online` and `IssuedAt`; `Close`
    just goes offline. **CLI:** `clients register <name>` / `register --self` /
    `clients retire <id>`, and `clients list` gains the presence column.

- `pkg/bus`: **the per-client credential allow-list — the unforgeable author**
  (ADR-0019). Each minted credential now carries a per-client JWT allow-list
  scoped to its bus-minted ULID: it may publish **only** under its own call
  prefix (`sx.api.<id>.>`) and subscribe **only** to its own delivery space
  (`sx.deliver.<id>.>`) plus its own per-client request/reply inbox
  (`_INBOX.<id>.>`, with a matching `nats.CustomInboxPrefix` on the SDK
  connection — a per-client inbox, not the shared `_INBOX.>`, so one client
  cannot eavesdrop on another's call replies). Because the subject token a client
  publishes a call under is now exactly the identity NATS authenticated, the
  author the bus stamps from it **cannot be forged** — and with this flip nothing
  reaches the messages stream, the KV buckets, or the control space except by
  asking the bus over a call. This is the last slice of
  the "nothing direct" cutover: the data plane (messages + artifacts) and the
  connect handshake already flowed through the bus; the credential is now precise
  rather than deny-only.
- `pkg/bus` + `pkg/sextant`: **the connect handshake moves through the bus**
  (ADR-0019). The SDK no longer touches the backend directly at connect: it
  registers with a `clients.register` call (the bus writes the directory record,
  keyed by the client's authenticated id and stamped with the bus clock), leaves
  with `clients.deregister` on `Close`, and the **protocol-epoch hard-gate is
  folded into register** — the call returns the bus epoch, which the SDK
  exact-matches, plus the bus-stamped `connected_at` for the clock-skew announce.
  Cooperative **drain now delivers over each client's own push space**
  (`sx.deliver.<id>.drain`) instead of a broadcast on `sx.control.*`, so a client
  needs no permission beyond its delivery subscription to receive it; the bus
  targets an in-memory connected set (authoritative, not the eventually-consistent
  registry). This completes the "nothing direct" cutover for the whole data plane
  **and** the connect handshake; only the per-client credential allow-list flip
  (which makes the deny-only guardrail precise, and the stamped author
  unforgeable) remains. The SDK's `Client` no longer holds a JetStream handle.
- `pkg/bus` + `pkg/sextant`: **the push-stream operations and the artifact
  cutover** (ADR-0019). The bus now serves `message.subscribe` and
  `artifact.watch` as server-side relays into a client's private delivery space
  (`sx.deliver.<clientID>.<subID>`), each ended by a `subscription.stop` control
  op (or by bus shutdown, which cancels them all). The SDK's `Subscribe` /
  `WatchArtifact` and **all** artifact methods (`CreateArtifact`,
  `UpdateArtifact`, `GetArtifact`, `DeleteArtifact`) now go through Wire API
  **calls** instead of the backend directly — the artifact ops moved as one unit
  because the bus stores artifacts as frames at rest. With this slice the whole
  data plane (messages + artifacts) flows through the bus; what remains is the
  per-client credential **allow-list flip** (which makes the stamped author
  unforgeable and routes the connect handshake itself through calls), in the
  next slice. Crash-driven relay teardown — a client that never stops — is
  deferred to the liveness work (TASK-20), the same gap the clients registry has.
- `cmd/sextant`: **the operator/test CLI** — the human face of the operation
  surface and the M2 e2e harness (TASK-28). Commands with exact operation-name
  parity (no aliases): `publish`, `read` (cursor-pull via `FetchMessages`),
  `subscribe` (live stream), `clients list`, and `artifact create|update|get|
  delete|watch`, each `--json`-capable. A **conformance test** reads
  `protocol/methods.json` and asserts the CLI exposes exactly one command per
  operation and invents none — making "one surface, many faces" mechanical (the
  MCP server will extend the same test). Smoke-verified end to end: two clients
  exchange a message and collaborate on a compare-and-set artifact through the
  bus, the read frame carrying the publisher's bus-stamped ULID author.
- `pkg/sextant`: **the SDK begins speaking the Wire API** (ADR-0019 cutover).
  `Client.Publish` now sends a `message.publish` **call** to the bus (which stamps
  the frame and appends it) instead of publishing to the stream directly, and the
  new `Client.FetchMessages` pulls a batch via `message.read` (cursor + resume —
  the pull complement to `Subscribe`, and the SDK half of the test CLI's `read`).
  `Client.ListClients` now goes through the `clients.list` operation too. Since
  the bus reads the whole registry on every client's behalf, a single corrupt
  record now **skips quietly** rather than failing the listing for everyone (the
  bus also sources each id from its authoritative registry key). (`Subscribe` and
  the artifact methods complete the cutover in the push-stream entry above; only
  the credential allow-list flip then remains.)
- `pkg/bus`: the bus now **serves the protocol's operations** as calls over the
  Wire API (ADR-0018, ADR-0019). A client makes a NATS request to
  `sx.api.<clientID>.<operation>`; the bus serves it against the backend interface
  (`internal/backend`), **stamps the frame** (id ULID, author from the call's
  subject token, kind, epoch; artifacts also revision + createdAt/updatedAt), and
  replies. Request/reply operations land here — `message.publish`, `message.read`
  (cursor-pull), `artifact.create/update/get/delete`, `clients.list` — with
  bounded concurrent responders (no head-of-line blocking) and reply-after-ack.
  The push-stream operations (`message.subscribe`, `artifact.watch`) and the SDK
  cutover follow. `internal/wireapi` defines the call subject scheme + the
  per-operation request/response shapes shared by the bus and SDK.
- `internal/backend`: the backend interface — the semantic contract
  (`protocol/semantic-contract.md`) as one internal Go interface the bus
  implements the operations against: a durable, ordered, replayable log
  (`Append`/`Read`/`Subscribe`, cursor = a bus-opaque synthesized sequence) and
  versioned records (`Create`/`Put`/`CompareAndSet`/`Get`/`Delete`/`Watch`/`Keys`,
  CAS on revision). A deep module behind a narrow interface; frame semantics stay
  in the bus. `internal/backend/natsbackend` is the first module (JetStream + KV);
  `internal/backend/conformance` is the executable contract every backend module
  must pass — so a future Redis module is portable by construction. See ADR-0018,
  ADR-0019.

- Go module (`github.com/love-lena/sextant`) and the polyglot-monorepo skeleton.
- `pkg/wire`: the wire atom — the JSON `Envelope` (`{id, sender, kind, epoch,
  record}`), the protocol `Epoch`, ULID-timestamp skew validation (`CheckSkew`,
  enforced sender- and receiver-side), and the per-message epoch check
  (`CheckEpoch`). `Record` is typed `Lexicon` (a `json.RawMessage` alias today,
  a seam for adding validation later). See ADR-0006 and ADR-0010.
- `sextant up`: runs the embedded bus. `sextant token <client-id>`: mints a
  per-client credentials file.
- `pkg/bus`: an embedded NATS server (JetStream) under **decentralized JWT
  auth** — one operator, one `SEXTANT` account, and **one user JWT per client**,
  so every connection is a distinct, verified identity and every op is
  attributable. Bootstraps the reserved `sx_` buckets; applies the client-tier
  guardrail (deny bucket/stream lifecycle, `sx_system` writes, `sx.control.*`);
  `Drain` broadcasts the cooperative-drain message. See ADR-0007, ADR-0012.
- `pkg/sx`: the reserved-namespace names (`sx_` buckets, `sx.` subjects).
- `pkg/conninfo`: the `bus.json` discovery file (URL only; credentials are
  per-client creds files).
- `pkg/sextant`: the Go SDK. `Connect` runs the connect handshake —
  authenticate with the client's own credentials file (`Options.CredsPath`,
  minted by `sextant token`), the protocol-epoch hard gate, a clients-registry
  write, a soft clock-skew announcement, a cooperative-drain handler, and
  auto-reconnect; with `Client.Drained`, `Close`, and `ID`. The client id
  (registry key and envelope sender) is read from the credential itself, so it
  is exactly the identity the bus authenticated. See ADR-0008, ADR-0010, ADR-0012.
- `pkg/bus`: publishes the protocol epoch to the client-readable `sx_meta`
  bucket at bootstrap, so clients read and hard-gate on it at connect (ADR-0015).
- `pkg/bus`: the client TCP listener opens only **after** bootstrap completes,
  so a client can never connect into a half-ready bus and fail its epoch read;
  the bus's own operator connection is in-process.
- `pkg/bus`: `sextant token` / `MintClient` reject a duplicate or malformed
  client id — each client gets exactly one verified identity, so two clients can
  never silently share a registry key (issued ids are recorded under
  `<store>/issued`).
- `pkg/sextant`: the Messages primitive. `Client.Publish` wraps a record in a
  wire envelope and publishes it to the `msg.*` space (waiting for the stream
  ack); `Client.Subscribe` delivers matching messages via an ephemeral ordered
  consumer with client-controlled replay (`DeliverAll`), checking each against
  the bus-stamped clock and quarantining skew violations. `pkg/bus` provisions
  the durable `MESSAGES` stream at bootstrap; `pkg/sx` adds the `msg.*` subject
  helpers (`ChannelSubject`, `AgentSubject`). See ADR-0005, ADR-0006.
- `pkg/sextant`: the Artifacts primitive — named, versioned units of durable
  shared work whose `Record` is a **Lexicon** (typed JSON), the same content
  model as a message's `Record` (ADR-0016). `CreateArtifact`, `UpdateArtifact`
  (compare-and-set on revision — the single-author-at-a-time discipline),
  `GetArtifact`, `DeleteArtifact`, and `WatchArtifact` (current value then live
  changes, deletes included via `ArtifactChange.Deleted`); a write is rejected
  unless the record is a non-empty valid lexicon.
  `pkg/bus` provisions the `ARTIFACTS` KV bucket at bootstrap, keeping **64
  revisions** (the NATS KV maximum). See ADR-0005, ADR-0016.
- `pkg/sextant`: `Client.ListClients` and the public `ClientInfo` type — the
  read half of the clients registry, a presence-only self-maintained directory.
  Every client already self-registers on `Connect` and leaves on `Close` (the
  write half); `ListClients` returns everyone connected right now (sorted by
  id), where "listed" means "registered and hasn't cleanly left." The record is
  `{id, kind, epoch, sdk, connected_at}`; heartbeat, read-time liveness, and
  stale-entry reaping are deferred (TASK-20). See ADR-0004, ADR-0008.

### Security

- Hardened credential file handling (found by review while landing ADR-0021):
  - `cmd/sextant`: `clients register` with a display name that contains a path
    separator (the bus allows names like `a/b`) no longer fails to write — or
    escapes the store via `../x` — when `--out` is omitted; the default creds
    filename falls back to the minted ULID for path-bearing names, so a successful
    mint never strands its credential.
  - `pkg/bus`: the operator/enrollment infra credentials are now (re)provisioned
    via an atomic owner-only (`0600`) write (temp file + rename). `os.WriteFile`
    left an existing file's looser mode intact, so a reused or user-supplied store
    with a world-readable leftover could keep high-privilege creds — which
    authorize identity issuance and retirement — group/world-readable.

### Changed

- **Re-pointing an established principal now takes `--force`** (ADR-0031) — `sextant
  principal set <ulid>` refuses to move an already-designated principal without
  `--force`, and prints the `current → new` change; the first claim of an unclaimed
  principal needs no flag. A move is announced through the existing `principal.watch`
  relay and written to the bus log. The first designation and reads are unchanged.
- **Client identity is now a bus-minted ULID + a `display_name`** (ADR-0019, the
  §3 review decision). `sextant token <display-name>` (was `<client-id>`) now
  mints a fresh ULID as the client's primary id — bus-owned, unforgeable — and
  carries the human `display_name` in the credential (a JWT tag). `Client.ID()`
  returns the ULID; the new `Client.DisplayName()` returns the label. The clients
  registry is keyed by the ULID, and `ClientInfo` / `clients.list` carry both id
  and `display_name`. Display names are unique by convention, not enforced by the
  bus (so duplicate-id minting no longer errors — each mint is a distinct ULID).
- `pkg/sx`: renamed the bus "channel" convention to **topic** — `ChannelSubject`
  → `TopicSubject` and the subject namespace `msg.chan.<name>` →
  `msg.topic.<name>`. A topic is a named room (a naming convention over the
  messages space, not a bus construct); "channel" is reserved for the Claude
  Code harness push mechanism, to avoid the two colliding. See ADR-0017.
- `pkg/sx`: renamed direct addressing `msg.agent.<id>` → `msg.client.<id>`
  (`AgentSubject` → `ClientSubject`). "Client" is the universal term; "agent" is
  not a Sextant concept. See ADR-0018 / CONTEXT.md.
- `pkg/wire`: renamed the wire atom `Envelope` → **`Frame`** and its `sender`
  field → **`author`**, and unified messages and artifacts under one frame —
  added the `artifact` kind and the bus-stamped artifact fields (`revision`,
  `createdAt`, `updatedAt`). The frame is the bus-stamped wrapper around a record
  (record = user space, frame = bus space); `author` is the authenticated
  identity the bus stamps, not a client-set field. `pkg/sextant`'s
  `Message.Envelope` is now `Message.Frame`. First step of implementing
  ADR-0018 / ADR-0019.

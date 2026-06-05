# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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

### Changed

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

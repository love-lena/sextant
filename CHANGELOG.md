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

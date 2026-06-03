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

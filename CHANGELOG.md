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
  (`CheckEpoch`). See ADR-0006 and ADR-0010.

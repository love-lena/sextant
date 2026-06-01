// Package sextantproto holds every type that appears on the sextant bus or
// in NATS KV. Go is the source of truth; JSON Schemas under schemas/ are
// regenerated from these types via `go generate ./pkg/sextantproto/...`
// and are consumed by the TypeScript client and any other reader.
//
// Spec: specs/protocols/envelope-schema.md, specs/protocols/bus-subjects.md,
// specs/protocols/rpc-catalog.md.
package sextantproto

// ProtoVersion is the current envelope protocol version. Minor bumps are
// additive only; major bumps require a parallel subject namespace.
//
// Aligned with the binary semver in the top-level VERSION file so a single
// number tracks both surfaces during the pre-1.0 stabilization phase. Once
// the wire format and the binary semver have meaningfully different
// evolution rates we may split them — see follow-up ticket on wire-format
// negotiation.
const ProtoVersion = "0.5.0"

// WireEpoch is the machine-checked compatibility key for the bus wire
// format (control-plane RFC §5.8). Unlike ProtoVersion — a cosmetic,
// human-facing string — WireEpoch is a single monotonically-increasing
// integer that MUST bump whenever a breaking change lands in any wire
// shape (removed/renamed field, type change, optional→required, removed
// enum value).
//
// It is the source of truth: the generator stamps it into the
// schemas/wire.json manifest, the TS codegen emits it as WIRE_EPOCH, and
// it is consumed three ways (RFC §5.8):
//   - AUTHORING: the CI schema-compat gate fails a breaking schema diff
//     that did not bump this integer.
//   - STALE AGENT: the reconciler compares each container's wire_epoch
//     label against this value (drift → converge by restart).
//   - STALE PEER: the admission front door rejects an RPC envelope whose
//     epoch does not match.
//
// Bump this by exactly one when (and only when) a breaking wire change
// lands. Additive changes (new optional field, new enum value, new
// message) do NOT bump it.
//
// Epoch 2 (control-plane P0, feat-ctl-p0-reconcile-spine): the
// AgentDefinition record was split into spec/status (RFC §5.2,
// Appendix C). The top-level `lifecycle`, `runtime`, `sandbox`, `tools`,
// `host_pin`, and `current_incarnation_id` fields were removed/relocated
// under the new `spec` / `status` objects — a breaking wire change a
// peer on the epoch-1 schema would misread, so the epoch advances. The
// converge-by-restart model (RFC §3) expects exactly this on a breaking
// agent-record change.
const WireEpoch = 2

// Regenerate the wire contract. The first directive walks the Go structs
// into schemas/*.json + schemas/wire.json (the source for proto_version /
// WireEpoch / closed enums). The second drives the TypeScript codegen off
// those schemas (types.generated.ts + the generated proto_version.ts), so
// `go generate ./...` regenerates BOTH sides of the Go↔TS contract with no
// hand-sync. Directives run in file order, so schemas land before the TS
// step reads them. The TS step is skipped if the npm workspace deps are
// absent (a Go-only checkout) — CI's `npm run codegen` re-runs it and
// asserts the committed output is in sync.
//
//go:generate go run github.com/love-lena/sextant/cmd/sextantproto-gen -out ./schemas
//go:generate go run github.com/love-lena/sextant/cmd/sextantproto-gen -ts -ts-dir ../../clients/typescript

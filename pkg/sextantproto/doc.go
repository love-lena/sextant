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
const ProtoVersion = "0.4.0"

//go:generate go run github.com/love-lena/sextant/cmd/sextantproto-gen -out ./schemas

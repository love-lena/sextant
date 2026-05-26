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
const ProtoVersion = "1.0"

//go:generate go run github.com/love-lena/sextant/cmd/sextantproto-gen -out ./schemas

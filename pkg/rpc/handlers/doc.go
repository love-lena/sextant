// Package handlers implements the M7 RPC verbs. Each handler is a
// closure-style constructor that takes the dependencies it needs
// (NATS KV bucket handle, ClickHouse driver.Conn, sextantd address,
// etc.) and returns an rpc.Handler the dispatcher calls per request.
//
// Wiring lives in cmd/sextantd. Handlers themselves are pure library
// code so they unit-test against fakes.
//
// Spec: specs/protocols/rpc-catalog.md "Verb payloads — M7 initial set".
// Plan: plans/bootstrap.md#M7.
package handlers

// Package client is the Go client library for sextant. It connects to
// NATS via the loopback operator listener, exposes typed subscriptions
// over JetStream streams, and offers KV and RPC access.
//
// M7 surface (full read + write path):
//
//   - Connect, Close
//   - Subscribe, SubscribeFromSeq (typed Message channel with JetStream
//     stream + consumer sequence populated, so consumers can resume)
//   - WatchKV, GetKV, PutKV
//   - Publish (validated Envelope publish on a bus subject)
//   - RPC (typed request/reply with idempotency + timeout + RPCError)
//   - Query (backed by the query_history RPC against ClickHouse)
//
// Spec: specs/components/client-libraries.md.
// Plan: plans/bootstrap.md#M7.
package client

// Package client is the Go client library for sextant. It connects to
// NATS via the loopback operator listener, exposes typed subscriptions
// over JetStream streams, and offers read-only access to NATS KV
// buckets.
//
// M4 ships the read path only:
//
//   - Connect, Close
//   - Subscribe, SubscribeFromSeq (typed Message channel with JetStream
//     stream + consumer sequence populated, so consumers can resume)
//   - WatchKV, GetKV
//
// The write path (Publish, RPC, PutKV) and the ClickHouse-backed Query
// land in M7. Query is exported in M4 but always returns
// ErrNotImplementedYet so callers fail fast instead of silently
// receiving an empty slice.
//
// Spec: specs/components/client-libraries.md.
// Plan: plans/bootstrap.md#M4.
package client

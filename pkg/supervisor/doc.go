// Package supervisor watches a long-running subprocess and restarts it
// with exponential backoff on unexpected exit. After a configurable
// number of consecutive restart failures, the supervised unit moves to
// "quarantine" — auto-restart stops and the operator must intervene.
//
// The package is generic over the supervised unit: callers supply a
// StartFn that returns a Process (something that can Wait + Kill). The
// supervisor does not itself spawn os/exec processes; it operates on
// whatever Process the StartFn returns. This keeps the package easy to
// test and reusable for NATS, ClickHouse, shipper, and future units.
//
// Plan: plans/bootstrap.md#M5
package supervisor

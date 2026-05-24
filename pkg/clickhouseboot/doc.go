// Package clickhouseboot starts a single-host clickhouse-server
// subprocess, applies sextant's schema migrations idempotently, and
// hands back a *Server the caller can dial.
//
// Spec: specs/components/clickhouse.md.
// Plan: plans/bootstrap.md#M3.
package clickhouseboot

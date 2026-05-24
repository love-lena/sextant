// Package shipper subscribes to NATS subjects, decodes Envelopes, and
// writes the rows out to ClickHouse with at-least-once delivery.
//
// The shipper is a separate process (`cmd/sextant-shipper/`) for failure
// isolation: a shipper crash must not take down sextantd. The operator
// (or a process supervisor) runs `sextant-shipper` alongside sextantd.
//
// Per-subject routing lives in mapping.go. Each subject pattern feeds a
// per-table batch builder, which flushes to ClickHouse on the smaller of
// a configurable time interval (default 100ms) or batch size (default
// 1000 events). When ClickHouse is unreachable the batch falls through
// to a BoltDB spillover at `~/.local/share/sextant/shipper-buffer/`. A
// drain goroutine retries spillover entries against ClickHouse in FIFO
// order. The shipper acks each JetStream message only after the row is
// durably written (to ClickHouse or to the BoltDB pending bucket).
//
// On hitting the buffer hard cap (10 GiB by default) the shipper fails
// closed: it drains its NATS consumers, emits a critical
// `audit.shipper_backpressure` envelope, and exits non-zero. Operator
// intervention is required. There is no silent drop unless
// `shipper.degraded_mode = "drop_oldest"` is set in the config.
//
// Spec: specs/components/shipper.md
// Plan: plans/bootstrap.md#M6
package shipper

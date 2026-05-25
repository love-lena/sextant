// Package shipperboot starts and supervises the sextant-shipper
// subprocess from inside sextantd.
//
// sextant-shipper is a separate binary that subscribes to NATS, decodes
// audit envelopes, and writes them to ClickHouse with at-least-once
// delivery. It runs as its own process for failure isolation: a
// ClickHouse-side hiccup must not stall the daemon. shipperboot is the
// sextantd-side wrapper that exec's it, owns the process-group lifecycle
// (SIGTERM → SIGKILL on the whole pgroup, mirroring natsboot /
// clickhouseboot), and surfaces a *Server the daemon's supervisor loop
// can drive.
//
// Spec: specs/components/shipper.md.
// Issue: plans/issues/feat-shipper-auto-supervise.md.
package shipperboot

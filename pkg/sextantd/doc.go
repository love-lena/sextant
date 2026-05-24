// Package sextantd holds the daemon-side types shared between the
// `sextant` operator CLI (init, doctor) and the `sextantd` daemon. It
// owns:
//
//   - Config struct and TOML loader (Load, Save)
//   - DefaultConfig: filesystem-rooted defaults
//   - Layout helpers: resolved paths, mode invariants
//   - RuntimeInfo: the small file the daemon writes after startup so
//     other tools can find the live NATS/ClickHouse ports.
//
// The daemon's actual lifecycle (subprocess start/stop, supervision,
// signal handling) lives in cmd/sextantd; this package is the shared
// substrate both the CLI and the daemon read.
//
// Plan: plans/bootstrap.md#M5
package sextantd

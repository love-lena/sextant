// Package version exposes the sextant build version. It is the smallest
// real package in the workspace and serves as the M0 smoke target — a
// package that compiles, lints, and tests cleanly proves the toolchain.
package version

// Version is the human-readable sextant build identifier. Phase 1 keeps
// this static; later milestones may override it at build time via -ldflags.
const Version = "0.0.0-dev"

// String returns the version identifier. Provided so callers do not depend
// on the exported constant directly, which keeps later -ldflags overrides
// transparent to consumers.
func String() string {
	return Version
}

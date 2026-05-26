// Package version exposes the sextant build version. It is the smallest
// real package in the workspace and serves as the M0 smoke target — a
// package that compiles, lints, and tests cleanly proves the toolchain.
package version

// Version is the human-readable sextant build identifier. Phase 1 keeps
// this static; later milestones may override it at build time via -ldflags.
const Version = "0.0.0-dev"

// GitSHA is the workspace HEAD recorded at build time. It is overridden by
// the Makefile via `-ldflags "-X github.com/love-lena/sextant/pkg/
// version.GitSHA=<sha>"`. An empty value means the binary was built without
// the linker flag (e.g. `go build` straight from source); doctor treats
// that case as "no embedded SHA, skip the staleness check".
//
// Declared as a var (not a const) so the linker can patch it. Do not assign
// from Go code at runtime — the linker is the only legitimate writer.
var GitSHA = ""

// String returns the version identifier. Provided so callers do not depend
// on the exported constant directly, which keeps later -ldflags overrides
// transparent to consumers.
func String() string {
	return Version
}

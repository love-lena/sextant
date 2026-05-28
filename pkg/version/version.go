// Package version exposes the sextant build version and commit SHA. It is
// the smallest real package in the workspace and serves as the M0 smoke
// target — a package that compiles, lints, and tests cleanly proves the
// toolchain.
//
// Version + Commit are both injected at link time by the Makefile via
// `-ldflags "-X github.com/love-lena/sextant/pkg/version.Version=..."`.
// Source of truth for Version is the top-level VERSION file; Commit is
// `git rev-parse --short HEAD`. Both fall back to safe defaults when the
// binary is built without -ldflags (`go run` / `go test`), so the package
// is always usable.
package version

// Version is the semver-formatted sextant build identifier (e.g. "v0.2.0").
// Injected at link time from the top-level VERSION file via -ldflags.
// Falls back to "dev" for un-ldflagged builds (`go run`, `go test`).
//
// Declared as a var (not a const) so the linker can patch it. Do not assign
// from Go code at runtime — the linker and tests are the only legitimate
// writers.
var Version = "dev"

// Commit is the short git SHA recorded at build time, injected via
// `-ldflags "-X github.com/love-lena/sextant/pkg/version.Commit=<sha>"`.
// Falls back to "unknown" when no SHA was injected.
//
// Display-oriented; for the doctor staleness check (which needs the full
// SHA to compare against `git rev-parse HEAD`) use GitSHA instead.
var Commit = "unknown"

// GitSHA is the full workspace HEAD recorded at build time, used by
// `sextant doctor` to detect stale installed binaries. Overridden by the
// Makefile via `-ldflags "-X github.com/love-lena/sextant/pkg/
// version.GitSHA=<sha>"`. An empty value means the binary was built without
// the linker flag (e.g. `go build` straight from source); doctor treats
// that case as "no embedded SHA, skip the staleness check".
//
// Declared as a var (not a const) so the linker can patch it. Do not assign
// from Go code at runtime — the linker is the only legitimate writer.
var GitSHA = ""

// String returns the version identifier. Provided so callers do not depend
// on the exported variable directly, which keeps later -ldflags overrides
// transparent to consumers.
func String() string {
	return Version
}

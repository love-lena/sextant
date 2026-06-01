// version.go owns the `sextantd version` subcommand. The version string and
// commit SHA are populated via -ldflags at build time by the Makefile; a
// `go run` / `go test` binary falls back to "dev" / "unknown".
//
// Unlike `sextant`, sextantd doesn't use cobra — it parses os.Args directly
// with the stdlib `flag` package. The subcommand dispatch lives here as a
// small helper so main.go can route on the first positional arg before any
// daemon startup happens.
//
// Spec: slug:feat-semver-versioning
package main

import (
	"fmt"
	"io"

	"github.com/love-lena/sextant/pkg/version"
)

// runVersion writes `<Version> (<Commit>)` to w and returns nil. Mirrors
// the `sextant version` output format so operators get a single shape
// across the CLI and the daemon.
func runVersion(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s (%s)\n", version.Version, version.Commit)
	return err
}

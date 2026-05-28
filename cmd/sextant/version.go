// version.go owns the `sextant version` subcommand. The version string and
// commit SHA are populated via -ldflags at build time by the Makefile; a
// `go run` / `go test` binary falls back to "dev" / "unknown".
//
// Spec: plans/issues/feat-semver-versioning.md
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/version"
)

// newVersionCmd renders the sextant version. Single-line, scriptable.
// `sextant --version` is also wired by Fang.
//
// Output format: `<Version> (<Commit>)`. Both halves are populated from
// pkg/version which the Makefile patches via -ldflags. Operators who pipe
// this into other tools rely on the format staying stable; treat any change
// as a documented break.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sextant version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", version.Version, version.Commit)
			return err
		},
	}
}

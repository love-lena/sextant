// sextant-schema-compat is the AUTHORING limb of the WireEpoch
// compatibility check (control-plane RFC §5.8). It diffs a baseline
// snapshot of pkg/sextantproto/schemas/ against the current one and fails
// the build if a BREAKING wire change landed without a WireEpoch bump.
//
// It is a sibling to the `changelog entry required` gate: the changelog
// gate guards the human-facing contract, this one guards the wire
// contract, and both are mechanical.
//
// Usage:
//
//	sextant-schema-compat -old <baseline-dir> [-new <current-dir>]
//
// -new defaults to pkg/sextantproto/schemas. The CI workflow materializes
// the baseline (the PR's merge-base committed schemas) into -old. Exit 0 =
// additive-only or epoch bumped; exit 1 = breaking change without a bump
// (with the offending changes printed); exit 2 = usage / IO error.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/love-lena/sextant/pkg/wirecompat"
)

func main() {
	oldDir := flag.String("old", "", "directory holding the baseline schema snapshot (required)")
	newDir := flag.String("new", "pkg/sextantproto/schemas", "directory holding the current schema snapshot")
	flag.Parse()

	if *oldDir == "" {
		fmt.Fprintln(os.Stderr, "sextant-schema-compat: -old <baseline-dir> is required")
		flag.Usage()
		os.Exit(2)
	}

	res, err := wirecompat.Compare(*oldDir, *newDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sextant-schema-compat: %v\n", err)
		os.Exit(2)
	}

	if len(res.Breaking) == 0 {
		fmt.Printf("schema-compat: no breaking wire changes (epoch %d → %d). OK\n", res.OldEpoch, res.NewEpoch)
		return
	}

	fmt.Printf("schema-compat: %d breaking wire change(s) detected (epoch %d → %d):\n", len(res.Breaking), res.OldEpoch, res.NewEpoch)
	for _, b := range res.Breaking {
		fmt.Printf("  - %s\n", b)
	}

	if res.NeedsBump() {
		fmt.Fprintf(os.Stderr, "\n::error::breaking wire change without a WireEpoch bump. "+
			"Bump sextantproto.WireEpoch (currently %d) in pkg/sextantproto/doc.go, "+
			"re-run `go generate ./...`, and commit. See RFC §5.8.\n", res.OldEpoch)
		os.Exit(1)
	}

	fmt.Printf("\nWireEpoch advanced %d → %d alongside the breaking change. OK\n", res.OldEpoch, res.NewEpoch)
}

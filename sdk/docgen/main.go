// Command docgen regenerates the mdbook reference's generated pages from canon
// (protocol/*.json, the implementer canon markdown, and go doc over pkg/sextant).
// It is run by `make book` before `mdbook build`, and by the mdbook CI job, which
// fails if regeneration produces a diff — so the docs never drift from canon.
package main

import (
	"log"

	"github.com/love-lena/sextant/sdk/docgen/internal/docgen"
)

//go:generate go run .

func main() {
	if err := docgen.Run(""); err != nil {
		log.Fatalf("docgen: %v", err)
	}
}

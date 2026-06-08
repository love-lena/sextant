package surface_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// forbidden lists the import-path fragments a surface must never reach for.
// Surfaces are public-SDK-only (ADR-0023): they build on the theme, the widgets,
// the busfeed adapter, and the public SDK (pkg/sextant and the public wire atom),
// and must not leak NATS or any internal package into the dash. An import whose
// path contains any of these fragments fails the build.
//
// pkg/sextant and pkg/wire are deliberately allowed — they are the public SDK
// surface and the public wire atom. Only NATS and internal packages are barred.
var forbidden = []string{
	"/internal/",
	"nats",
}

// TestNoForbiddenImports parses every Go file in this package directory and fails
// if any import path contains a forbidden fragment. This is the CI-checkable form
// of AC-2 ("public SDK only").
func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.Contains(path, bad) {
					t.Errorf("%s imports forbidden path %q (matches %q)", name, path, bad)
				}
			}
		}
	}
	// Guard against a vacuous pass: if the walk found nothing, fail loudly.
	if scanned == 0 {
		t.Fatal("found no .go files to scan")
	}
}

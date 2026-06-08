package theme_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// forbidden lists the import-path fragments the theme package must never reach
// for. The theme is library-only presentation code (ADR-0014, ADR-0023): no SDK,
// no internal packages, no NATS.
var forbidden = []string{
	"pkg/sextant",
	"/internal/",
	"nats",
}

// TestNoForbiddenImports parses every Go file in this package directory and
// fails if any import path contains a forbidden fragment.
func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var goFiles int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		goFiles++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.Contains(path, bad) {
					t.Errorf("%s imports forbidden path %q (matches %q)", name, path, bad)
				}
			}
		}
	}
	if goFiles == 0 {
		t.Fatal("found no .go files to scan")
	}
}

package widget_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbidden lists the import-path fragments a dash widget must never reach for.
// Widgets are domain-free, library-only code (ADR-0023): no SDK, no internal
// packages, no NATS. An import whose path contains any of these fragments fails
// the build.
var forbidden = []string{
	"pkg/sextant",
	"/internal/",
	"nats",
}

// TestNoForbiddenImports parses every Go file in this package directory and
// fails if any import path contains a forbidden fragment. This is the
// CI-checkable form of the AC "widgets import no SDK".
func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
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
}

// TestPackageDirExists is a guard: if the directory walk above somehow found no
// files, the import check would vacuously pass. Confirm we actually scanned this
// package.
func TestPackageDirExists(t *testing.T) {
	if _, err := filepath.Abs("."); err != nil {
		t.Fatalf("resolve package dir: %v", err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var goFiles int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			goFiles++
		}
	}
	if goFiles == 0 {
		t.Fatal("found no .go files to scan")
	}
}

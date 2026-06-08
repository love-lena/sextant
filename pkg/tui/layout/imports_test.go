package layout_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbidden lists the import-path fragments the layout package must never reach
// for. The layout is domain-free (ADR-0023): it composes surfaces against the
// Surface contract alone — no SDK, no internal packages, no NATS. An import whose
// path contains any of these fragments fails the build. (pkg/sextant is the SDK;
// reaching for it would mean domain logic is leaking into the layout, the smell
// the task warns against.)
var forbidden = []string{
	"pkg/sextant",
	"/internal/",
	"nats",
}

// TestNoForbiddenImports parses every non-test Go file in the package directory
// and fails if any import path contains a forbidden fragment. This is the
// CI-checkable form of "the layout imports no SDK". Test files are excluded: the
// gallery + goldens legitimately import the SDK to build mock data, but the
// package's production code must not.
func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
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

// TestPackageDirExists guards against a vacuous pass: if the walk found no
// non-test Go files, the import check would trivially succeed. Confirm we
// scanned real package code.
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
		n := e.Name()
		if strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			goFiles++
		}
	}
	if goFiles == 0 {
		t.Fatal("found no production .go files to scan")
	}
}

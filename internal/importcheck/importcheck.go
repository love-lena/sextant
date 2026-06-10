// Package importcheck is test support for the TUI bright lines (ADR-0023): it
// loads a package's transitive PRODUCTION dependency closure via `go list
// -deps` and asserts the import disciplines the pkg/tui strata promise.
//
// The closure is computed on the non-test package, which closes the two holes
// a per-file direct-import scan leaves open: a forbidden dependency is caught
// even when it arrives transitively through an allowed project import, and
// test-only harness imports (an embedded bus, a fixture builder) never widen
// the production allowlist — what is asserted is exactly what links into a
// real binary.
//
// The package is itself stdlib-only, so importing it from a _test.go file adds
// nothing to any production closure and violates none of the rules it checks.
package importcheck

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Module is the module path the rules are phrased against.
const Module = "github.com/love-lena/sextant"

const (
	// sdkPkg is the public SDK — the one sanctioned road to the bus.
	sdkPkg = Module + "/pkg/sextant"
	// busPkg is the embedded-NATS server wrapper. It is a harness concern
	// (tests, `sextant up`) and never a TUI dependency, in any closure.
	busPkg = Module + "/pkg/bus"
	// wireAtom is the SDK's wire-shape implementation detail: the only
	// module-internal package an SDK-facing closure may contain, and only as
	// pkg/sextant's own import.
	wireAtom = Module + "/internal/wireapi"
	// internalNS prefixes every module-internal package.
	internalNS = Module + "/internal/"
	// natsNS prefixes every NATS package (client, jwt, nkeys, …).
	natsNS = "github.com/nats-io/"
)

// Closure returns the transitive production dependency closure of pkgPath:
// every package `go list -deps` reports for the non-test package, keyed by
// import path, each carrying its direct imports. It fails the test on a load
// error and guards against a vacuous result.
func Closure(t *testing.T, pkgPath string) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-json=ImportPath,Imports", pkgPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkgPath, err, stderr.String())
	}
	deps := make(map[string][]string)
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode `go list -deps -json` output for %s: %v", pkgPath, err)
		}
		deps[p.ImportPath] = p.Imports
	}
	if _, ok := deps[pkgPath]; !ok || len(deps) < 2 {
		t.Fatalf("vacuous closure for %s: %d packages (root present: %v)", pkgPath, len(deps), ok)
	}
	return deps
}

// AssertPresentationOnly pins the presentation stratum's bright line (theme,
// widget): library-only code whose production closure contains no SDK, no
// pkg/bus, no module-internal package, and no NATS at all — not even
// transitively through an allowed project import.
func AssertPresentationOnly(t *testing.T, pkgPath string) {
	t.Helper()
	for dep := range Closure(t, pkgPath) {
		switch {
		case dep == sdkPkg:
			t.Errorf("%s: production closure contains the SDK (%s)", pkgPath, dep)
		case dep == busPkg:
			t.Errorf("%s: production closure contains %s (the embedded-NATS wrapper)", pkgPath, dep)
		case strings.HasPrefix(dep, internalNS):
			t.Errorf("%s: production closure contains internal package %s", pkgPath, dep)
		case strings.HasPrefix(dep, natsNS):
			t.Errorf("%s: production closure contains NATS package %s", pkgPath, dep)
		}
	}
}

// AssertSDKOnly pins the bus-facing stratum's bright line (busfeed, surface,
// and — through the surface contract — layout): the bus is reached through the
// public SDK alone. In the production closure:
//   - pkg/bus (the embedded-NATS wrapper) never appears;
//   - the only module package that may directly import a NATS package is
//     pkg/sextant, so NATS is reachable ONLY via the SDK;
//   - the only module-internal package allowed is internal/wireapi (the SDK's
//     wire atom), and only pkg/sextant may import it.
func AssertSDKOnly(t *testing.T, pkgPath string) {
	t.Helper()
	for dep, imports := range Closure(t, pkgPath) {
		if dep == busPkg {
			t.Errorf("%s: production closure contains %s (the embedded-NATS wrapper)", pkgPath, dep)
		}
		if strings.HasPrefix(dep, internalNS) && dep != wireAtom {
			t.Errorf("%s: production closure contains internal package %s", pkgPath, dep)
		}
		if !strings.HasPrefix(dep, Module+"/") {
			continue // the per-edge discipline below binds module packages only
		}
		for _, imp := range imports {
			if strings.HasPrefix(imp, natsNS) && dep != sdkPkg {
				t.Errorf("%s: %s imports %s directly; NATS is reached only via %s", pkgPath, dep, imp, sdkPkg)
			}
			if strings.HasPrefix(imp, internalNS) && dep != sdkPkg {
				t.Errorf("%s: %s imports %s directly; internal packages are the SDK's alone", pkgPath, dep, imp)
			}
		}
	}
}

// AssertNoDirectImport fails when pkgPath's production code itself directly
// imports any of the forbidden paths, whatever the rest of the closure says.
// The layout uses it to stay domain-free at its own boundary: its closure
// reaches the SDK only because the Surface CONTRACT lives in pkg/tui/surface,
// never because layout code touched the SDK or the feed adapter.
func AssertNoDirectImport(t *testing.T, pkgPath string, forbidden ...string) {
	t.Helper()
	direct := Closure(t, pkgPath)[pkgPath]
	for _, imp := range direct {
		for _, f := range forbidden {
			if imp == f {
				t.Errorf("%s directly imports %s", pkgPath, f)
			}
		}
	}
}

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
	sdkPkg = Module + "/clients/go/sdk"
	// busNS prefixes the one Go bus server and its internals (the embedded-NATS
	// wrapper, the backend). The bus is a harness concern (tests, `sextant up`)
	// and never a TUI dependency, in any closure.
	busNS = Module + "/bus"
	// protocolNS prefixes the language-neutral protocol bindings (wire, sx,
	// conninfo, wireapi). They are the protocol's shared Go layer, imported by
	// both the bus and the clients; an SDK-facing closure may contain any of
	// them. wire/sx/conninfo are freely importable (they were public pkg/
	// packages before the move); wireAtom (below) keeps its SDK-only edge.
	protocolNS = Module + "/protocol/"
	// wireAtom is the SDK's wire-shape binding (was internal/wireapi). It is the
	// one protocol package an SDK-facing closure reaches only as the SDK's own
	// import — never imported directly by a TUI stratum. (The other protocol
	// bindings carry no such edge.)
	wireAtom = Module + "/protocol/wireapi"
	// tuiNS prefixes the TUI strata (theme, widget, busfeed, surface, layout) —
	// the presentation/feed library layer under test. The strata legitimately
	// import each other (widget→theme, surface→busfeed), so they are the one
	// in-module family allowed alongside the SDK and the protocol bindings.
	tuiNS = Module + "/clients/go/apps/internal/tui/"
	// natsNS prefixes every NATS package (client, jwt, nkeys, …).
	natsNS = "github.com/nats-io/"
)

// modulePkg reports whether dep is a package of this module.
func modulePkg(dep string) bool { return strings.HasPrefix(dep, Module+"/") }

// sdkLayer reports whether dep is a sanctioned SDK-facing module package: the
// SDK itself, a protocol binding, or a TUI-stratum package. Any OTHER module
// package in an SDK-facing closure (the bus, an app-private internal, a
// convention) is the bright-line violation the strata checks catch.
func sdkLayer(dep string) bool {
	return dep == sdkPkg || strings.HasPrefix(dep, protocolNS) || strings.HasPrefix(dep, tuiNS)
}

// Closure returns the transitive production dependency closure of pkgPath:
// every package `go list -deps` reports for the non-test package, keyed by
// import path, each carrying its direct imports. It fails the test on a load
// error and guards against a vacuous result.
func Closure(t *testing.T, pkgPath string) map[string][]string {
	t.Helper()
	deps := closureLenient(t, pkgPath)
	if _, ok := deps[pkgPath]; !ok || len(deps) < 2 {
		t.Fatalf("vacuous closure for %s: %d packages (root present: %v)", pkgPath, len(deps), ok)
	}
	return deps
}

// closureLenient is Closure without the vacuous-result guard: it still fails on
// a load or decode error, but tolerates a single-package closure (a placeholder
// package with no imports). AssertConventionDeps uses it so the empty
// conventions/ placeholder is trivially compliant rather than a hard failure.
func closureLenient(t *testing.T, pkgPath string) map[string][]string {
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
	return deps
}

// AssertPresentationOnly pins the presentation stratum's bright line (theme,
// widget): library-only code whose production closure contains no SDK, no
// protocol binding, no bus, no app-private internal, and no NATS at all — not
// even transitively through an allowed project import. The only in-module
// packages it may reach are sibling TUI strata (tuiNS).
func AssertPresentationOnly(t *testing.T, pkgPath string) {
	t.Helper()
	for dep := range Closure(t, pkgPath) {
		switch {
		case dep == sdkPkg:
			t.Errorf("%s: production closure contains the SDK (%s)", pkgPath, dep)
		case strings.HasPrefix(dep, busNS):
			t.Errorf("%s: production closure contains the bus (%s)", pkgPath, dep)
		case strings.HasPrefix(dep, protocolNS):
			t.Errorf("%s: production closure contains protocol binding %s", pkgPath, dep)
		case strings.HasPrefix(dep, natsNS):
			t.Errorf("%s: production closure contains NATS package %s", pkgPath, dep)
		case modulePkg(dep) && !strings.HasPrefix(dep, tuiNS):
			t.Errorf("%s: production closure contains non-presentation package %s", pkgPath, dep)
		}
	}
}

// AssertSDKOnly pins the bus-facing stratum's bright line (busfeed, surface,
// and — through the surface contract — layout): the bus is reached through the
// public SDK alone. In the production closure:
//   - the bus (busNS — the embedded-NATS wrapper and its backend) never appears;
//   - the only package that may directly import a NATS package is the SDK
//     (NATS's own intra-family imports aside), so NATS is reachable ONLY via
//     the SDK — third-party dependencies are held to this edge rule too;
//   - the only in-module packages allowed are the sanctioned SDK layer (the SDK,
//     the protocol bindings, and the TUI strata; see sdkLayer); an app-private
//     internal or any other module package is a violation, and only the SDK may
//     directly import a protocol binding.
func AssertSDKOnly(t *testing.T, pkgPath string) {
	t.Helper()
	for dep, imports := range Closure(t, pkgPath) {
		if strings.HasPrefix(dep, busNS) {
			t.Errorf("%s: production closure contains the bus (%s)", pkgPath, dep)
		}
		if modulePkg(dep) && !sdkLayer(dep) {
			t.Errorf("%s: production closure contains non-SDK-layer package %s", pkgPath, dep)
		}
		for _, imp := range imports {
			// The NATS edge rule binds every dep, third-party included — a
			// dependency pulling in NATS would be a second NATS path in the
			// linked binary even with our own packages clean. NATS packages
			// importing their own siblings are the SDK's dependency, not a
			// second path.
			if strings.HasPrefix(imp, natsNS) && dep != sdkPkg && !strings.HasPrefix(dep, natsNS) {
				t.Errorf("%s: %s imports %s directly; NATS is reached only via %s", pkgPath, dep, imp, sdkPkg)
			}
			// The wire atom is the SDK's alone: only the SDK may import it
			// directly (the old internal/wireapi edge, now at protocol/wireapi).
			// The other protocol bindings (wire/sx/conninfo) were public pkg/
			// packages and carry no such edge. Module-only by nature — the
			// compiler already forbids foreign modules from importing our
			// packages; the wire atom's own deps (e.g. protocol/wire) are the
			// SDK's wire layer, not a second path.
			if modulePkg(dep) && imp == wireAtom && dep != sdkPkg && dep != wireAtom {
				t.Errorf("%s: %s imports %s directly; the wire atom is the SDK's alone", pkgPath, dep, imp)
			}
		}
	}
}

// AssertConventionDeps pins the convention stratum's bright line (ADR-0041): a
// convention library (clients/go/conventions/…) is an engine-as-a-library over
// the SDK — anything it does, a bare client could do over the operations. So
// its production closure may reach the SDK and the protocol bindings, but NEVER
// the bus (busNS): a convention that touched the embedded server would be a bus
// feature in disguise. NATS is likewise reachable only via the SDK.
//
// It reads the closure leniently: a placeholder convention package with no
// imports (the conventions/ tree until the goals library lands) has a
// single-package closure and is trivially compliant — there is nothing yet for
// the rule to forbid. The rule bites on every convention library as it lands.
func AssertConventionDeps(t *testing.T, pkgPath string) {
	t.Helper()
	for dep, imports := range closureLenient(t, pkgPath) {
		if strings.HasPrefix(dep, busNS) {
			t.Errorf("%s: a convention reaches the bus (%s); conventions are libraries over the SDK, never the bus", pkgPath, dep)
		}
		for _, imp := range imports {
			if strings.HasPrefix(imp, natsNS) && dep != sdkPkg && !strings.HasPrefix(dep, natsNS) {
				t.Errorf("%s: %s imports %s directly; a convention reaches NATS only via %s", pkgPath, dep, imp, sdkPkg)
			}
		}
	}
}

// AssertBusImportsNoClients pins the foundation's bright line (ADR-0041): the
// one Go bus stands under the co-equal clients and never depends on one. Its
// production closure must contain no clients/ package — not the SDK, not a
// convention, not an app. (The CLI app embeds the bus, not the reverse; that
// edge is allowed and runs the other direction.)
func AssertBusImportsNoClients(t *testing.T, pkgPath string) {
	t.Helper()
	const clientsNS = Module + "/clients/"
	for dep := range Closure(t, pkgPath) {
		if strings.HasPrefix(dep, clientsNS) {
			t.Errorf("%s: the bus reaches a client package (%s); the bus never imports clients", pkgPath, dep)
		}
	}
}

// AssertNoDirectImport fails when pkgPath's production code itself directly
// imports any of the forbidden paths, whatever the rest of the closure says.
// The layout uses it to stay domain-free at its own boundary: its closure
// reaches the SDK only because the Surface CONTRACT lives in
// clients/go/apps/internal/tui/surface, never because layout code touched the
// SDK or the feed adapter.
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

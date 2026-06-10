package layout_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestImportClosure pins the layout's bright line (ADR-0023) on the package's
// TRANSITIVE production closure. The layout composes panes against the Surface
// contract, and that contract lives in pkg/tui/surface — so the closure
// legitimately reaches the SDK (surface → busfeed → pkg/sextant). The line is
// therefore in two parts:
//
//   - the SDK-only discipline on the whole closure: no pkg/bus, and NATS and
//     internal/wireapi appear only as pkg/sextant's own imports;
//   - domain-freedom at the layout's OWN boundary: layout code never imports
//     the SDK or the feed adapter directly — its only road there is the
//     surface contract.
//
// The check runs on the non-test package, so the gallery/golden fixtures'
// SDK imports (legitimate, _test.go-only) never widen the production
// allowlist.
func TestImportClosure(t *testing.T) {
	const pkg = importcheck.Module + "/pkg/tui/layout"
	importcheck.AssertSDKOnly(t, pkg)
	importcheck.AssertNoDirectImport(
		t, pkg,
		importcheck.Module+"/pkg/sextant",
		importcheck.Module+"/pkg/tui/busfeed",
	)
}

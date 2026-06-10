package surface_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestImportClosure pins the surfaces' bright line (ADR-0023, AC-2: "public
// SDK only") on the package's TRANSITIVE production closure: a surface builds
// on the theme, the widgets, the busfeed adapter, and the public SDK — and the
// bus is reached through pkg/sextant alone. No pkg/bus, no direct NATS import
// anywhere but the SDK, no module-internal package beyond the SDK's own wire
// atom — even through an allowed project import. The check runs on the
// non-test package, so a test harness import can never widen the production
// allowlist.
func TestImportClosure(t *testing.T) {
	importcheck.AssertSDKOnly(t, importcheck.Module+"/pkg/tui/surface")
}

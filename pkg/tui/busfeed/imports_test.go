package busfeed_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestImportClosure pins the adapter's bright line (ADR-0023, AC-3: "public
// SDK only, no bus/NATS types leak") on the package's TRANSITIVE production
// closure: the bus is reached through pkg/sextant alone — no pkg/bus (the
// embedded-NATS wrapper this package's own TESTS legitimately import for the
// harness; the closure check deliberately excludes _test.go files, so that
// import no longer hides a hole in the production line), no direct NATS
// import anywhere but the SDK, and no module-internal package beyond the
// SDK's own wire atom.
func TestImportClosure(t *testing.T) {
	importcheck.AssertSDKOnly(t, importcheck.Module+"/pkg/tui/busfeed")
}

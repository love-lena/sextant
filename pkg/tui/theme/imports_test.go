package theme_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestImportClosure pins the theme's bright line (ADR-0014, ADR-0023) on the
// package's TRANSITIVE production closure: the theme is library-only
// presentation code — no SDK, no pkg/bus, no module-internal package, no NATS —
// and the closure check holds that even through an allowed project import. It
// checks the non-test package, so a test harness import can never widen the
// production allowlist.
func TestImportClosure(t *testing.T) {
	importcheck.AssertPresentationOnly(t, importcheck.Module+"/pkg/tui/theme")
}

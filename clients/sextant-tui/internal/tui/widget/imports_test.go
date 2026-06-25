package widget_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestImportClosure pins the widgets' bright line (ADR-0023) on the package's
// TRANSITIVE production closure: widgets are domain-free, library-only code —
// no SDK, no protocol binding, no bus, no NATS, and no in-module package but a
// sibling TUI stratum — and the closure
// check holds that even through an allowed project import (a widget pulling in
// the theme cannot smuggle anything past the line). It checks the non-test
// package, so a test harness import can never widen the production allowlist.
func TestImportClosure(t *testing.T) {
	importcheck.AssertPresentationOnly(t, importcheck.Module+"/clients/sextant-tui/internal/tui/widget")
}

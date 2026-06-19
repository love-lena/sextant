package conventions_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConventionDeps pins the convention stratum's bright line (ADR-0041): a
// convention library is an engine over the SDK — its production closure may
// reach the SDK and the protocol bindings but NEVER the bus. The check runs on
// the conventions package's transitive closure, so the rule bites on every
// convention library as it lands here. (The package is a placeholder today, so
// the closure is trivially clean; the assertion is in place for the goals
// convention and those that follow.)
func TestConventionDeps(t *testing.T) {
	importcheck.AssertConventionDeps(t, importcheck.Module+"/clients/go/conventions")
}

package goals_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConventionDeps pins the goals convention's bright line (ADR-0041): the
// goals library is an engine over the SDK — anything it does, a bare client could
// do over the primitive operations. Its production closure may reach the SDK and
// the protocol bindings, but NEVER the bus (a convention that touched the embedded
// server would be a bus feature in disguise), and NATS only via the SDK.
//
// This asserts on the REAL library (clients/go/conventions/goals), whose closure
// is non-trivial (it pulls in the protocol bindings via conv/goals → sx), so the
// rule actually bites here — distinct from the parent conventions/ placeholder
// assertion, whose closure is empty. Self-verified to fail when goals.go imports
// the bus (see the PR notes): temporarily add `_ "…/bus"` to goals.go and this
// test goes red.
func TestConventionDeps(t *testing.T) {
	importcheck.AssertConventionDeps(t, importcheck.Module+"/clients/go/conventions/goals")
}

package review_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConventionDeps pins the review convention's bright line (ADR-0041): the
// review library is an engine over the SDK — anything it does, a bare client could
// do over the primitive operations. Its production closure may reach the SDK, the
// protocol bindings, and the sibling goals convention (for the approve→met closed
// loop), but NEVER the bus (a convention that touched the embedded server would be
// a bus feature in disguise), a client, or a host helper; NATS only via the SDK.
//
// The sibling import of conventions/goal/go is allowed: conventions/ is a stratum
// distinct from clients/, so AssertConventionDeps (which forbids bus/clients/shared)
// permits one convention building on another — exactly as conv-review depends on
// conv-goals in TS.
func TestConventionDeps(t *testing.T) {
	importcheck.AssertConventionDeps(t, importcheck.Module+"/conventions/review/go")
}

package spawn_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConventionDeps pins the spawn convention's bright line (ADR-0041): the spawn
// library is an engine over the SDK — its single verb issues one message.publish a
// bare client could issue. Its production closure may reach the SDK and the protocol
// bindings but NEVER the bus, a client, or a host helper; NATS only via the SDK.
func TestConventionDeps(t *testing.T) {
	importcheck.AssertConventionDeps(t, importcheck.Module+"/conventions/spawn/go")
}

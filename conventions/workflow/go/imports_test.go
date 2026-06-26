package workflow_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConventionDeps pins the workflow convention's bright line (ADR-0041): the
// workflow library is an engine over the SDK — its start verb issues one
// message.publish a bare client could issue. Its production closure may reach the
// SDK and the protocol bindings but NEVER the bus, a client, or a host helper; NATS
// only via the SDK.
func TestConventionDeps(t *testing.T) {
	importcheck.AssertConventionDeps(t, importcheck.Module+"/conventions/workflow/go")
}

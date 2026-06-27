package agentactivity_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConventionDeps pins the agent.activity convention's bright line (ADR-0041):
// the parse side is a pure record + subject helper over the standard library — its
// production closure reaches neither the SDK nor the bus. The assertion holds the
// line as the package grows (a future producer-side verb must still stay SDK-only).
func TestConventionDeps(t *testing.T) {
	importcheck.AssertConventionDeps(t, importcheck.Module+"/conventions/agentactivity/go")
}

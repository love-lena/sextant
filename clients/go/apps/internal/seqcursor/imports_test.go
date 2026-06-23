package seqcursor_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestCursorSitesDelegate (TASK-182 AC#3, second clause): the three durable
// per-subject cursor sites — the MCP substate, the attest hook, and violet's ack
// watermark — must reach the shared seqcursor module rather than re-declare their
// own atomic-temp-rename JSON cursor. The rule bites per site.
//
// This is its own bite-witness: it was RED before the refactor (each site owned a
// private cursor and pulled in no seqcursor) and goes GREEN exactly when all three
// delegate, so the predicate is real, not vacuous. Self-verified by reverting one
// site to its private cursor and watching its sub-test fail (see the PR notes).
func TestCursorSitesDelegate(t *testing.T) {
	for _, p := range []string{
		importcheck.Module + "/clients/go/apps/mcp",
		importcheck.Module + "/clients/go/apps/mcp/attest",
		importcheck.Module + "/clients/go/apps/violet/internal/violet",
	} {
		t.Run(p, func(t *testing.T) { importcheck.AssertUsesSeqCursor(t, p) })
	}
}

// TestAppsNoWireAtom (TASK-182 AC#4): no client app / convention / app-internal
// reaches the wire atom (protocol/wireapi) in its production closure — the SDK is
// its sole sanctioned importer, so the publish-output leak (and any future one)
// cannot reappear. The bus legitimately imports wireapi but is not a clients/
// package, so it is outside the rule by construction.
//
// Run over the app packages that drive the bus; the SDK is excluded by the rule
// itself (it IS the sanctioned importer). The companion vacuity guard
// TestWireAtomRuleBites (internal/importcheck) proves the SDK's closure really
// holds the wireAtom edge, so this rule cannot pass merely because nothing imports
// wireapi anywhere.
func TestAppsNoWireAtom(t *testing.T) {
	for _, p := range []string{
		importcheck.Module + "/clients/go/apps/mcp",
		importcheck.Module + "/clients/go/apps/workflow",
		importcheck.Module + "/clients/go/apps/violet",
		importcheck.Module + "/clients/go/apps/violet/internal/violet",
		importcheck.Module + "/clients/go/apps/internal/dash",
		importcheck.Module + "/clients/go/apps/internal/dashapi",
		importcheck.Module + "/clients/go/apps/internal/dashserve",
	} {
		t.Run(p, func(t *testing.T) { importcheck.AssertNoWireAtom(t, p) })
	}
}

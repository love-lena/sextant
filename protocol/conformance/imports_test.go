package conformance_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestConformanceImportsNoClients pins the layout constraint that puts the
// runner in client land (ADR-0041, TASK-183): the language-neutral
// protocol/conformance package — the vector format and its data types — must
// never reach a client. If it did, the protocol would import a convention verb,
// inverting the dependency the runner's placement in clients/go/conformance
// exists to avoid. The check runs on the production closure, so a client import
// arriving transitively fails here too.
func TestConformanceImportsNoClients(t *testing.T) {
	importcheck.AssertProtocolImportsNoClients(t, importcheck.Module+"/protocol/conformance")
}

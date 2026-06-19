package bus_test

import (
	"testing"

	"github.com/love-lena/sextant/internal/importcheck"
)

// TestBusImportsNoClients pins the foundation's bright line (ADR-0041): the one
// Go bus stands under the co-equal clients and never depends on one. The check
// runs on the bus's transitive production closure, so a client import that
// arrived through any project package — not only a direct one — fails here.
func TestBusImportsNoClients(t *testing.T) {
	importcheck.AssertBusImportsNoClients(t, importcheck.Module+"/bus")
}

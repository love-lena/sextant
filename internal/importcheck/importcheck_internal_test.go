package importcheck

import (
	"strings"
	"testing"
)

// TestNewEdgesBite guards against the two ADR-0041 edge checks going vacuous.
// AssertBusImportsNoClients and AssertConventionDeps would silently pass if
// their closures were trivial or their predicates never matched anything real,
// so this self-test pins the discriminating facts on the live tree:
//
//   - the bus's production closure is substantial and reaches NATS (it is a
//     real, non-trivial closure), and contains no clients/ package — the
//     bus-imports-no-clients rule is satisfiable and meaningful;
//   - the sextant CLI's closure DOES contain a clients/ package, so the
//     predicate the bus rule fires on is a thing that genuinely occurs — the
//     rule would catch a bus that grew such an edge.
func TestNewEdgesBite(t *testing.T) {
	const clientsNS = Module + "/clients/"

	busClosure := Closure(t, Module+"/bus")
	var sawNATS, sawClient bool
	for dep, imports := range busClosure {
		if strings.HasPrefix(dep, clientsNS) {
			sawClient = true
		}
		for _, imp := range imports {
			if strings.HasPrefix(imp, natsNS) {
				sawNATS = true
			}
		}
	}
	if sawClient {
		t.Fatalf("bus closure already reaches a clients/ package — the bus rule's subject is violated")
	}
	if !sawNATS {
		t.Fatalf("bus closure reaches no NATS package — closure looks trivial, the rule may be vacuous")
	}

	// The CLI embeds the bus AND drives clients, so its closure is the witness
	// that a clients/ dependency is a real, detectable shape — the bus rule
	// would fire on a bus that grew such an edge.
	cliClosure := Closure(t, Module+"/clients/go/apps/sextant")
	witnessClient := false
	for dep := range cliClosure {
		if strings.HasPrefix(dep, clientsNS) {
			witnessClient = true
			break
		}
	}
	if !witnessClient {
		t.Fatalf("CLI closure contains no clients/ package — cannot witness that the bus rule's predicate matches real code")
	}

	// Witness the convention rule's predicate: the bus's own closure contains a
	// busNS package (itself), so a convention library whose closure reached the
	// bus would trip AssertConventionDeps. (The conventions/ placeholder is
	// empty today, so its own test passes trivially; this proves the rule has a
	// real shape to catch once a convention lands.)
	witnessBus := false
	for dep := range busClosure {
		if strings.HasPrefix(dep, busNS) {
			witnessBus = true
			break
		}
	}
	if !witnessBus {
		t.Fatalf("bus closure contains no busNS package — cannot witness that the convention rule's predicate matches real code")
	}
}

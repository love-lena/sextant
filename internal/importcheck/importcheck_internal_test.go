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
	cliClosure := Closure(t, Module+"/clients/sextant-cli")
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

// TestClientIsolation pins the ADR-0049 client bright line on the live tree: the
// Go clients are flat peers, so no client's production closure reaches another —
// except the one sanctioned edge, the CLI embedding the dash server. The witness
// for non-vacuity is TestNewEdgesBite (the CLI closure genuinely reaches a
// clients/ package), so a rule that stopped firing would be caught there.
func TestClientIsolation(t *testing.T) {
	// The CLI embeds the dash server — the one allowed cli→dash edge.
	AssertClientIsolation(t, Module+"/clients/sextant-cli", Module+"/clients/sextant-dash")
	for _, c := range []string{
		"sextant-dash", "sextant-tui", "dispatcher",
		"coordinator", "assistant", "sextant-mcp",
	} {
		AssertClientIsolation(t, Module+"/clients/"+c)
	}
}

// TestSDKImportsNoShared pins the ADR-0049 host-portability line: the SDK reaches
// no shared/go host helper. The witness keeps it from going vacuous — a real
// client (the CLI) DOES reach shared/go, so the forbidden shape genuinely occurs
// and the rule would catch the SDK growing such an edge.
func TestSDKImportsNoShared(t *testing.T) {
	AssertSDKImportsNoShared(t, sdkPkg)

	sawShared := false
	for dep := range Closure(t, Module+"/clients/sextant-cli") {
		if strings.HasPrefix(dep, sharedNS) {
			sawShared = true
			break
		}
	}
	if !sawShared {
		t.Fatalf("no clients/ package reaches a shared/ helper — cannot witness that AssertSDKImportsNoShared's predicate matches real code")
	}
}

// TestWireAtomRuleBites guards AssertNoWireAtom (TASK-182 AC#4) against going
// vacuous. The rule would pass green if nothing imported the wire atom anywhere,
// so this self-test pins the discriminating facts on the live tree:
//
//   - the SDK's production closure DOES reach the wire atom (protocol/wireapi) —
//     the edge the rule forbids elsewhere is a real, occurring shape that the SDK
//     legitimately holds, so the rule has something to catch if it moved;
//   - the rule's predicate is non-trivial: the wire atom is a module package and
//     is not the SDK itself, so AssertNoWireAtom's guard (skip the SDK and the
//     wire atom, flag every other module package importing it) is meaningful.
func TestWireAtomRuleBites(t *testing.T) {
	sdkClosure := Closure(t, sdkPkg)
	if _, ok := sdkClosure[wireAtom]; !ok {
		t.Fatalf("SDK closure does not reach %s — the wire atom edge looks gone, so AssertNoWireAtom may be vacuous", wireAtom)
	}

	// The discriminating predicate: the wire atom is a module package distinct
	// from the SDK, so the rule fires on a NON-SDK module package that imports it.
	if !modulePkg(wireAtom) {
		t.Fatalf("%s is not recognised as a module package — AssertNoWireAtom's subject is malformed", wireAtom)
	}
	if wireAtom == sdkPkg {
		t.Fatalf("the wire atom and the SDK collapsed to one path — AssertNoWireAtom would exclude everything")
	}

	// Witness that SOME module package directly imports the wire atom today (the
	// SDK), proving the imports-scan the rule walks matches a real edge. Without
	// such an importer the rule's inner loop never runs and is silently vacuous.
	sawImporter := false
	for dep, imports := range sdkClosure {
		if !modulePkg(dep) {
			continue
		}
		for _, imp := range imports {
			if imp == wireAtom {
				sawImporter = true
				break
			}
		}
	}
	if !sawImporter {
		t.Fatalf("no module package directly imports %s in the SDK closure — cannot witness the edge AssertNoWireAtom scans for", wireAtom)
	}
}

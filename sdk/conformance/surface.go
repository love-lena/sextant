package conformance

import (
	"os"
	"sort"
	"testing"

	pconf "github.com/love-lena/sextant/protocol/conformance"
)

// AssertVectorOpsInMethods is the protocol-surface guarantee, extended from
// name-set parity to the transcripts: every `op` used across every vector under
// dir must be an operation methods.json declares. A vector that emits an op the
// protocol does not define is a malformed vector — it pins behaviour against a
// call the bus cannot serve. This is the transcript-era successor to the
// existing methods.json name-set conformance test: that test asserted the CLI
// and MCP surfaces match the operation NAMES; this asserts the vector
// transcripts only ever name real operations. The CLI/MCP name-parity tests
// stay where they are (they guard a different surface); this one guards the
// vectors, so together they are the full surface check, not a duplicate.
//
// methodsPath is the path to protocol/methods.json (the caller resolves it,
// e.g. filepath.Join("..","..","protocol","methods.json")).
func AssertVectorOpsInMethods(t *testing.T, vectorsDir, methodsPath string) {
	t.Helper()
	data, err := os.ReadFile(methodsPath)
	if err != nil {
		t.Fatalf("read methods.json (%s): %v", methodsPath, err)
	}
	declared, err := pconf.MethodsOps(data)
	if err != nil {
		t.Fatalf("parse methods.json: %v", err)
	}
	if len(declared) == 0 {
		t.Fatal("methods.json declares no operations")
	}

	conventions, err := pconf.AllConventions(vectorsDir)
	if err != nil {
		t.Fatalf("list conventions under %s: %v", vectorsDir, err)
	}
	used := map[string]bool{}
	for _, conv := range conventions {
		vectors, err := pconf.LoadOpTranscripts(vectorsDir, conv)
		if err != nil {
			t.Fatalf("load %q vectors: %v", conv, err)
		}
		for _, lv := range vectors {
			for _, op := range lv.Vector.Operations {
				if !declared[op.Op] {
					t.Errorf("%s: operation %q is not declared in methods.json", lv.Path, op.Op)
				}
				used[op.Op] = true
			}
		}
	}

	// Report the covered surface so a reviewer sees which operations the vectors
	// actually exercise — coverage is observable, not assumed. (It is informational,
	// not an assertion: a low-level op like clients.register has no convention verb,
	// so the vectors will not name every operation, and should not be forced to.)
	if len(used) > 0 {
		names := make([]string, 0, len(used))
		for op := range used {
			names = append(names, op)
		}
		sort.Strings(names)
		t.Logf("conformance vectors exercise %d/%d operations: %v", len(used), len(declared), names)
	}
}

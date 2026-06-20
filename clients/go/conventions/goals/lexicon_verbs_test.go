package goals_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pconf "github.com/love-lena/sextant/protocol/conformance"
)

// TestLexiconVerbMatchesVector keeps the lexicon's documentary verb signature
// honest against the real verb (AC#1: "record + verb signatures defined once in
// the lexicon"). The lexicon's verbs.setCriterion.operations is prose a TS
// implementer reads; the recorded vector is what the Go verb actually emits. This
// asserts the two agree on the OPERATION SEQUENCE — so the lexicon can't quietly
// drift from the verb (e.g. the verb gains a step the lexicon never documents).
// It is a cheap structural check, not a full schema validator.
func TestLexiconVerbMatchesVector(t *testing.T) {
	// The lexicon's declared operation sequence for setCriterion (each entry starts
	// with the primitive op name, e.g. "artifact.get goal.<goalId>").
	lexOps := lexiconVerbOps(t, "setCriterion")
	if len(lexOps) == 0 {
		t.Fatal("lexicon goal.json declares no verbs.setCriterion.operations")
	}

	// The recorded vector's actual emitted ops.
	vecOps := vectorOps(t, "setCriterion")

	if len(lexOps) != len(vecOps) {
		t.Fatalf("operation count: lexicon declares %d, vector records %d\n lexicon: %v\n vector:  %v",
			len(lexOps), len(vecOps), lexOps, vecOps)
	}
	for i, want := range vecOps {
		// The lexicon entry is prose beginning with the op name; assert it leads with
		// the op the vector records, in order.
		if !strings.HasPrefix(lexOps[i], want) {
			t.Errorf("operation %d: vector records %q, lexicon declares %q (must lead with the op name)", i, want, lexOps[i])
		}
	}
}

// lexiconVerbOps reads verbs.<verb>.operations (the declared op sequence) from the
// goal lexicon.
func lexiconVerbOps(t *testing.T, verb string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "protocol", "lexicons", "goal.json"))
	if err != nil {
		t.Fatalf("read goal.json: %v", err)
	}
	// defs.verbs holds the verb entries alongside scalar metadata (type,
	// description), so decode it as raw members and pull out the named verb object
	// rather than a uniform map.
	var lf struct {
		Defs struct {
			Verbs map[string]json.RawMessage `json:"verbs"`
		} `json:"defs"`
	}
	if err := json.Unmarshal(raw, &lf); err != nil {
		t.Fatalf("parse goal.json: %v", err)
	}
	rawVerb, ok := lf.Defs.Verbs[verb]
	if !ok {
		t.Fatalf("lexicon goal.json has no verbs.%s", verb)
	}
	var sig struct {
		Operations []string `json:"operations"`
	}
	if err := json.Unmarshal(rawVerb, &sig); err != nil {
		t.Fatalf("parse verbs.%s: %v", verb, err)
	}
	return sig.Operations
}

// vectorOps reads the recorded vector's op sequence (the op field of each
// operation) for a goals verb.
func vectorOps(t *testing.T, verb string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(vectorsDir(), "goals", verb+".json"))
	if err != nil {
		t.Fatalf("read %s vector: %v", verb, err)
	}
	var v pconf.OpTranscriptVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse %s vector: %v", verb, err)
	}
	ops := make([]string, len(v.Operations))
	for i, o := range v.Operations {
		ops[i] = o.Op
	}
	return ops
}

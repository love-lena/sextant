package docgen

import (
	"strings"
	"testing"
)

// repoRoot resolves the repo root from the test's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := findRoot()
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	return root
}

func TestOperationsPage(t *testing.T) {
	root := repoRoot(t)
	out, err := genOperations(root)
	if err != nil {
		t.Fatalf("genOperations: %v", err)
	}
	for _, want := range []string{
		"# Operations",
		"## `message.publish`",
		"**Delivery:** one-shot",
		"| Field | Type |",
		"## `clients.register`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("operations page missing %q", want)
		}
	}
}

func TestLexiconPages(t *testing.T) {
	root := repoRoot(t)
	frame, err := genFrame(root)
	if err != nil {
		t.Fatalf("genFrame: %v", err)
	}
	// The frame's author field carries the unforgeable-identity note.
	if !strings.Contains(frame, "`author`") || !strings.Contains(frame, "not forgeable") {
		t.Errorf("frame page missing the author field / its note:\n%s", frame)
	}
	if !strings.Contains(frame, "one of: message, artifact") {
		t.Errorf("frame page missing the kind knownValues")
	}

	recs, err := genRecordLexicons(root)
	if err != nil {
		t.Fatalf("genRecordLexicons: %v", err)
	}
	if !strings.Contains(recs, "## `chat.message`") || !strings.Contains(recs, "## `document`") {
		t.Errorf("records page missing a record lexicon section")
	}
}

func TestSDKPages(t *testing.T) {
	root := repoRoot(t)
	ref, err := genSDKReference(root)
	if err != nil {
		t.Fatalf("genSDKReference: %v", err)
	}
	if !strings.Contains(ref, "type `Client`") || !strings.Contains(ref, "func `Connect`") {
		t.Errorf("API reference missing Client/Connect")
	}

	msgs, err := genSDKMessages(root)
	if err != nil {
		t.Fatalf("genSDKMessages: %v", err)
	}
	if !strings.Contains(msgs, "(*Client) Publish") {
		t.Errorf("messages page missing Client.Publish")
	}
}

// TestDeterministic guards the drift-check: regenerating must be byte-stable.
func TestDeterministic(t *testing.T) {
	root := repoRoot(t)
	for _, gen := range []func(string) (string, error){
		genOperations, genRecordLexicons, genFrame, genRegistry,
		genSDKReference, genSDKMessages, genSDKArtifacts, genSDKClients,
	} {
		a, err := gen(root)
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		b, err := gen(root)
		if err != nil {
			t.Fatalf("gen (2nd): %v", err)
		}
		if a != b {
			t.Errorf("non-deterministic output (%d vs %d bytes)", len(a), len(b))
		}
	}
}

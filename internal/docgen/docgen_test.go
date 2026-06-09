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

func TestLexiconFragment(t *testing.T) {
	root := repoRoot(t)

	// The frame fragment is a table only — no conceptual prose, no H1 — so a
	// prose page can include it. It carries the field rows + knownValues.
	frame, err := lexiconFragment("frame.json")(root)
	if err != nil {
		t.Fatalf("frame fragment: %v", err)
	}
	if strings.Contains(frame, "# ") {
		t.Errorf("fragment must not contain a heading (it is included into a prose page):\n%s", frame)
	}
	for _, want := range []string{"| Field | Type | Required | Description |", "`author`", "one of: message, artifact"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame fragment missing %q", want)
		}
	}

	doc, err := lexiconFragment("document.json")(root)
	if err != nil {
		t.Fatalf("document fragment: %v", err)
	}
	if !strings.Contains(doc, "`title`") || !strings.Contains(doc, "`body`") {
		t.Errorf("document fragment missing its fields")
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
	gens := []func(string) (string, error){
		genOperations,
		genSDKReference, genSDKMessages, genSDKArtifacts, genSDKClients,
		lexiconFragment("chat.message.json"), lexiconFragment("document.json"),
		lexiconFragment("frame.json"), lexiconFragment("client.json"),
	}
	for _, gen := range gens {
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

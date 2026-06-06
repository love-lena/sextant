package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCLIMatchesOperations is the "one surface, many faces" guarantee (TASK-28):
// it reads protocol/methods.json — the source of truth — and asserts the CLI
// exposes exactly one command per operation, and no command that isn't an
// operation. Parity is mechanical, not a thing a reviewer has to remember.
func TestCLIMatchesOperations(t *testing.T) {
	path := filepath.Join("..", "..", "protocol", "methods.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Operations []struct {
			Name     string `json:"name"`
			Delivery string `json:"delivery"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse methods.json: %v", err)
	}
	if len(doc.Operations) == 0 {
		t.Fatal("methods.json declares no operations")
	}

	// Every operation has a CLI command.
	declared := make(map[string]bool, len(doc.Operations))
	for _, op := range doc.Operations {
		declared[op.Name] = true
		if cliOperations[op.Name] == "" {
			t.Errorf("operation %q (%s) has no CLI command", op.Name, op.Delivery)
		}
	}
	// The CLI invents no command that isn't an operation.
	for op := range cliOperations {
		if !declared[op] {
			t.Errorf("CLI command for %q has no matching operation in methods.json", op)
		}
	}
}

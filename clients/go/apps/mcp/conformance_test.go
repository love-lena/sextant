package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCPMatchesOperations extends the CLI's parity guarantee (TASK-28) to the
// MCP tool surface, in both directions: every operation in methods.json is a
// tool or excluded by declaration — and every exposed tool is a mapped
// operation or a declared extra. Push-stream tools must declare channel
// delivery. A new protocol op or a stray tool fails here until the mapping
// says where it lives.
func TestMCPMatchesOperations(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "protocol", "methods.json")
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

	tools := make(map[string]toolDef)
	for _, td := range toolDefs {
		if td.op != "" {
			tools[td.op] = td
		}
	}

	declared := make(map[string]string, len(doc.Operations))
	for _, op := range doc.Operations {
		declared[op.Name] = op.Delivery
		td, isTool := tools[op.Name]
		_, isExcluded := excludedOps[op.Name]
		switch {
		case isTool == isExcluded:
			t.Errorf("operation %q (%s): want exactly one of tool/excluded, got tool=%v excluded=%v",
				op.Name, op.Delivery, isTool, isExcluded)
		case isTool && (op.Delivery == "push-stream") != td.channel:
			t.Errorf("operation %q delivery=%s but tool %q channel=%v — push-stream tools deliver via the channel, others must not",
				op.Name, op.Delivery, td.name, td.channel)
		}
	}

	for _, td := range toolDefs {
		if td.op == "" {
			if !declaredExtras[td.name] {
				t.Errorf("tool %q maps to no operation and is not a declared extra", td.name)
			}
			continue
		}
		if _, ok := declared[td.op]; !ok {
			t.Errorf("tool %q maps to %q, which methods.json does not declare", td.name, td.op)
		}
		if want := strings.ReplaceAll(td.op, ".", "_"); td.name != want {
			t.Errorf("tool for %q is named %q, want the mechanical mapping %q", td.op, td.name, want)
		}
	}

	for op := range excludedOps {
		if _, ok := declared[op]; !ok {
			t.Errorf("excluded %q has no matching operation in methods.json", op)
		}
	}
}

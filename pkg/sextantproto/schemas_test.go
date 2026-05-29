package sextantproto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSchemasOnDisk asserts that the generator's outputs exist and parse as
// valid JSON. The full structural correctness check lives in the generator
// itself; this is the cheap guardrail that catches accidental deletion or
// truncation in a PR.
func TestSchemasOnDisk(t *testing.T) {
	dir := "schemas"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read schemas dir: %v", err)
	}
	want := map[string]bool{
		"envelope.json":                    false,
		"address.json":                     false,
		"agent_definition.json":            false,
		"agent_incarnation.json":           false,
		"agent_frame_payload.json":         false,
		"lifecycle_payload.json":           false,
		"audit_payload.json":               false,
		"user_input_request_payload.json":  false,
		"user_input_response_payload.json": false,
		"heartbeat_payload.json":           false,
		"rpc_request.json":                 false,
		"rpc_response.json":                false,
		"rpc_error.json":                   false,
		"span.json":                        false,
		"metric.json":                      false,
		"log_record.json":                  false,
		// RPC verb payloads (consumed by the TypeScript client).
		"list_agents_request.json":       false,
		"list_agents_response.json":      false,
		"get_agent_status_request.json":  false,
		"get_agent_status_response.json": false,
		"read_file_request.json":         false,
		"read_file_response.json":        false,
		"query_history_request.json":     false,
		"query_history_response.json":    false,
		// M11 agent-lifecycle verb payloads.
		"spawn_agent_request.json":   false,
		"spawn_agent_response.json":  false,
		"kill_agent_request.json":    false,
		"kill_agent_response.json":   false,
		"prompt_agent_request.json":  false,
		"prompt_agent_response.json": false,
		// M12 archive verb payloads.
		"archive_agent_request.json":  false,
		"archive_agent_response.json": false,
		// M12 verb payloads.
		"restart_agent_request.json":      false,
		"restart_agent_response.json":     false,
		"list_dir_request.json":           false,
		"list_dir_response.json":          false,
		"stat_request.json":               false,
		"stat_response.json":              false,
		"exec_in_container_request.json":  false,
		"exec_in_container_response.json": false,
		"query_audit_request.json":        false,
		"query_audit_response.json":       false,
		"query_trace_request.json":        false,
		"query_trace_response.json":       false,
		// Diagnostic verb (feat-doctor-show-daemon-version).
		"get_version_request.json":  false,
		"get_version_response.json": false,
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// wire.json is the contract-substrate manifest (proto_version,
		// WireEpoch, closed enums), not a JSON Schema — it has no $schema
		// key and is covered by TestWireManifestMatchesGoConstants.
		if e.Name() == "wire.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		if _, ok := parsed["$schema"]; !ok {
			t.Fatalf("%s missing $schema", e.Name())
		}
		if _, ok := want[e.Name()]; ok {
			want[e.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("required schema file missing: %s (run `go generate ./pkg/sextantproto/...`)", name)
		}
	}
}

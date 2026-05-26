// sextantproto-gen regenerates JSON Schema files under
// pkg/sextantproto/schemas/ from the Go types in pkg/sextantproto.
//
// It is invoked by `go generate ./pkg/sextantproto/...`. The output is
// stable across runs: schemas are written canonical-form (sorted keys),
// suffixed by a single trailing newline so git diffs stay clean.
//
// Plan: plans/bootstrap.md#M1
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"

	"github.com/invopop/jsonschema"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func main() {
	out := flag.String("out", "pkg/sextantproto/schemas", "output directory for JSON Schemas")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o750); err != nil {
		log.Fatalf("mkdir %s: %v", *out, err)
	}

	r := jsonschema.Reflector{
		AllowAdditionalProperties:  false,
		DoNotReference:             false,
		ExpandedStruct:             false,
		RequiredFromJSONSchemaTags: false,
		// Reflectors emit Go-style names by default; we want the JSON tag
		// to drive property names, which is the package's default behavior.
	}

	type entry struct {
		Name string
		T    any
	}

	entries := []entry{
		{"envelope", &sextantproto.Envelope{}},
		{"address", &sextantproto.Address{}},
		{"agent_definition", &sextantproto.AgentDefinition{}},
		{"agent_incarnation", &sextantproto.AgentIncarnation{}},
		{"agent_frame_payload", &sextantproto.AgentFramePayload{}},
		{"lifecycle_payload", &sextantproto.LifecyclePayload{}},
		{"audit_payload", &sextantproto.AuditPayload{}},
		{"user_input_request_payload", &sextantproto.UserInputRequestPayload{}},
		{"user_input_response_payload", &sextantproto.UserInputResponsePayload{}},
		{"heartbeat_payload", &sextantproto.HeartbeatPayload{}},
		{"rpc_request", &sextantproto.RPCRequest{}},
		{"rpc_response", &sextantproto.RPCResponse{}},
		{"rpc_error", &sextantproto.RPCError{}},
		{"span", &sextantproto.Span{}},
		{"metric", &sextantproto.Metric{}},
		{"log_record", &sextantproto.LogRecord{}},
		// RPC verb payloads — consumed by the TypeScript client codegen.
		{"list_agents_request", &sextantproto.ListAgentsRequest{}},
		{"list_agents_response", &sextantproto.ListAgentsResponse{}},
		{"get_agent_status_request", &sextantproto.GetAgentStatusRequest{}},
		{"get_agent_status_response", &sextantproto.GetAgentStatusResponse{}},
		{"read_file_request", &sextantproto.ReadFileRequest{}},
		{"read_file_response", &sextantproto.ReadFileResponse{}},
		{"query_history_request", &sextantproto.QueryHistoryRequest{}},
		{"query_history_response", &sextantproto.QueryHistoryResponse{}},
		{"spawn_agent_request", &sextantproto.SpawnAgentRequest{}},
		{"spawn_agent_response", &sextantproto.SpawnAgentResponse{}},
		{"kill_agent_request", &sextantproto.KillAgentRequest{}},
		{"kill_agent_response", &sextantproto.KillAgentResponse{}},
		{"prompt_agent_request", &sextantproto.PromptAgentRequest{}},
		{"prompt_agent_response", &sextantproto.PromptAgentResponse{}},
		{"archive_agent_request", &sextantproto.ArchiveAgentRequest{}},
		{"archive_agent_response", &sextantproto.ArchiveAgentResponse{}},
		// M12 verb payloads.
		{"restart_agent_request", &sextantproto.RestartAgentRequest{}},
		{"restart_agent_response", &sextantproto.RestartAgentResponse{}},
		{"list_dir_request", &sextantproto.ListDirRequest{}},
		{"list_dir_response", &sextantproto.ListDirResponse{}},
		{"stat_request", &sextantproto.StatRequest{}},
		{"stat_response", &sextantproto.StatResponse{}},
		{"exec_in_container_request", &sextantproto.ExecInContainerRequest{}},
		{"exec_in_container_response", &sextantproto.ExecInContainerResponse{}},
		{"query_audit_request", &sextantproto.QueryAuditRequest{}},
		{"query_audit_response", &sextantproto.QueryAuditResponse{}},
		{"query_trace_request", &sextantproto.QueryTraceRequest{}},
		{"query_trace_response", &sextantproto.QueryTraceResponse{}},
		// M14 verb payloads.
		{"worktree_info", &sextantproto.WorktreeInfo{}},
		{"worktree_create_request", &sextantproto.WorktreeCreateRequest{}},
		{"worktree_create_response", &sextantproto.WorktreeCreateResponse{}},
		{"worktree_destroy_request", &sextantproto.WorktreeDestroyRequest{}},
		{"worktree_destroy_response", &sextantproto.WorktreeDestroyResponse{}},
		{"worktree_list_request", &sextantproto.WorktreeListRequest{}},
		{"worktree_list_response", &sextantproto.WorktreeListResponse{}},
		{"worktree_merge_request", &sextantproto.WorktreeMergeRequest{}},
		{"worktree_merge_response", &sextantproto.WorktreeMergeResponse{}},
		{"worktree_diff_request", &sextantproto.WorktreeDiffRequest{}},
		{"worktree_diff_response", &sextantproto.WorktreeDiffResponse{}},
	}

	for _, e := range entries {
		schema := r.Reflect(e.T)
		if schema == nil {
			log.Fatalf("reflect %s (%s): nil schema", e.Name, reflect.TypeOf(e.T).String())
		}
		raw, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			log.Fatalf("marshal %s: %v", e.Name, err)
		}
		raw = append(raw, '\n')
		path := filepath.Join(*out, e.Name+".json")
		if err := writeIfChanged(path, raw); err != nil {
			log.Fatalf("write %s: %v", path, err)
		}
	}
	fmt.Printf("wrote %d schemas to %s\n", len(entries), *out)
}

// writeIfChanged writes data to path only if it differs from the current
// file contents. Keeps mtimes stable across no-op regenerations.
//
// path is constructed inside main() by joining the -out flag with a
// hard-coded file stem from the entries table; gosec G304 fires on the
// flag-derived path. JSON schemas are committed artifacts read by other
// tools (TS build, code review), so they must be world-readable, which
// trips G306. Both are intentional for this generator.
func writeIfChanged(path string, data []byte) error {
	existing, err := os.ReadFile(path) //nolint:gosec // path is generator-controlled
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // committed artifact, must be world-readable
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

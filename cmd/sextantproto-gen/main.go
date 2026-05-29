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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"

	"github.com/invopop/jsonschema"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func main() {
	out := flag.String("out", "pkg/sextantproto/schemas", "output directory for JSON Schemas")
	tsMode := flag.Bool("ts", false, "drive the TypeScript codegen (clients/typescript) instead of emitting Go schemas")
	tsDir := flag.String("ts-dir", "clients/typescript", "clients/typescript directory (used with -ts)")
	flag.Parse()

	if *tsMode {
		runTSCodegen(*tsDir)
		return
	}

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
		// Diagnostic verb (feat-doctor-show-daemon-version).
		{"get_version_request", &sextantproto.GetVersionRequest{}},
		{"get_version_response", &sextantproto.GetVersionResponse{}},
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

	// Emit the wire manifest: the single machine-readable source for the
	// proto version, the WireEpoch compatibility key, and the closed
	// enums. The TS codegen reads this to emit proto_version.ts (so
	// PROTO_VERSION / WIRE_EPOCH / KIND_* / ADDRESS_* are generated, not
	// hand-synced), and the CI schema-compat gate reads wire_epoch out of
	// it. RFC §5.8.
	if err := writeWireManifest(*out); err != nil {
		log.Fatalf("write wire manifest: %v", err)
	}

	fmt.Printf("wrote %d schemas + wire.json to %s\n", len(entries), *out)
}

// wireManifest is the machine-readable contract substrate emitted to
// schemas/wire.json. encoding/json marshals struct fields in declaration
// order, so the on-disk key order is stable across runs; both consumers
// (TS codegen, CI schema-compat gate) parse by key.
type wireManifest struct {
	ProtoVersion string   `json:"proto_version"`
	WireEpoch    int      `json:"wire_epoch"`
	Kinds        []string `json:"kinds"`
	AddressKinds []string `json:"address_kinds"`
	FrameKinds   []string `json:"frame_kinds"`
}

// writeWireManifest derives the manifest from the sextantproto package's
// exported source of truth and writes it canonical-form (sorted, single
// trailing newline) so git diffs stay clean and regeneration is
// idempotent.
func writeWireManifest(outDir string) error {
	m := wireManifest{
		ProtoVersion: sextantproto.ProtoVersion,
		WireEpoch:    sextantproto.WireEpoch,
		Kinds:        make([]string, 0, len(sextantproto.AllKinds())),
		AddressKinds: make([]string, 0, len(sextantproto.AllAddressKinds())),
		FrameKinds:   make([]string, 0, len(sextantproto.AllFrameKinds())),
	}
	for _, k := range sextantproto.AllKinds() {
		m.Kinds = append(m.Kinds, string(k))
	}
	for _, k := range sextantproto.AllAddressKinds() {
		m.AddressKinds = append(m.AddressKinds, string(k))
	}
	for _, k := range sextantproto.AllFrameKinds() {
		m.FrameKinds = append(m.FrameKinds, string(k))
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	raw = append(raw, '\n')
	return writeIfChanged(filepath.Join(outDir, "wire.json"), raw)
}

// runTSCodegen drives the TypeScript codegen (clients/typescript: emits
// types.generated.ts + the generated proto_version.ts from the schemas
// this generator just wrote). It is the second half of `go generate
// ./...` so both sides of the Go↔TS wire contract regenerate from one
// command.
//
// If the npm workspace deps are not installed (a Go-only checkout), it
// prints a skip notice and returns 0 rather than failing — CI installs the
// deps and re-runs `npm run codegen` with an in-sync assertion, so the
// gate still holds there. Missing `npm` is treated the same way.
func runTSCodegen(tsDir string) {
	if _, err := os.Stat(filepath.Join(tsDir, "node_modules")); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("ts codegen: %s/node_modules absent; skipping (run `npm ci` then re-run, or let CI do it)\n", tsDir)
		return
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		fmt.Println("ts codegen: npm not found on PATH; skipping (CI runs it)")
		return
	}
	cmd := exec.Command(npm, "run", "codegen") //nolint:gosec // fixed args, dir is generator-controlled
	cmd.Dir = tsDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("ts codegen (%s): %v", tsDir, err)
	}
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

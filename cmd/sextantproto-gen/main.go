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

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
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

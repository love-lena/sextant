// Package cliout tests pin the JSON contract.
package cliout_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/cliout"
)

// TestEnvelopeRoundTrip pins the on-the-wire shape:
//
//	{"data": ..., "meta": {"version": 1, "command": "agents.list"}}
//
// Renames, removals, or enum reorderings on any of these field names
// require bumping meta.version and gating behind --meta-version=2.
func TestEnvelopeRoundTrip(t *testing.T) {
	type row struct {
		Name string `json:"name"`
	}
	env := cliout.Envelope{
		Data: []row{{Name: "alpha"}, {Name: "beta"}},
		Meta: cliout.MetaInfo{Version: 1, Command: "agents.list"},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["data"]; !ok {
		t.Errorf("envelope must have a 'data' field; got %s", raw)
	}
	metaRaw, ok := generic["meta"]
	if !ok {
		t.Fatalf("envelope must have a 'meta' field; got %s", raw)
	}
	var meta cliout.MetaInfo
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if meta.Version != 1 {
		t.Errorf("meta.version = %d, want 1", meta.Version)
	}
	if meta.Command != "agents.list" {
		t.Errorf("meta.command = %q, want agents.list", meta.Command)
	}
}

// TestEnvelopeFromCommandConvertsPathToDotted ensures the cobra command
// path "sextant agents list" → "agents.list". This is the canonical
// dotted form documented in `conventions/tui-conventions.md` and in
// the feat-cli-output-protocol ticket.
func TestEnvelopeFromCommandConvertsPathToDotted(t *testing.T) {
	root := &cobra.Command{Use: "sextant"}
	agents := &cobra.Command{Use: "agents"}
	list := &cobra.Command{Use: "list"}
	root.AddCommand(agents)
	agents.AddCommand(list)

	env := cliout.EnvelopeFromCommand(list, []string{"a"})
	if env.Meta.Command != "agents.list" {
		t.Errorf("meta.command = %q, want agents.list", env.Meta.Command)
	}
	if env.Meta.Version != 1 {
		t.Errorf("meta.version = %d, want 1", env.Meta.Version)
	}
}

// TestEnvelopeFromCommandStripsRoot ensures the top-level "sextant"
// segment never shows up in meta.command — the dotted name is
// resource.verb, not sextant.resource.verb.
func TestEnvelopeFromCommandStripsRoot(t *testing.T) {
	cases := []struct {
		path string
		dots string
		want string
	}{
		{"sextant agents list", "agents.list", "agents.list"},
		{"sextant events tail", "events.tail", "events.tail"},
		{"sextant init", "init", "init"},
		{"sextant audit query", "audit.query", "audit.query"},
		{"sextant", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := cliout.DottedCommand(tc.path)
			if got != tc.want {
				t.Errorf("DottedCommand(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestWriteEnvelope confirms the helper writes a pretty-printed
// envelope with a trailing newline.
func TestWriteEnvelope(t *testing.T) {
	var buf bytes.Buffer
	env := cliout.Envelope{
		Data: []int{1, 2, 3},
		Meta: cliout.MetaInfo{Version: 1, Command: "x.y"},
	}
	if err := cliout.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("buf empty")
	}
	if buf.Bytes()[buf.Len()-1] != '\n' {
		t.Errorf("envelope output must end with newline; got %q", buf.String())
	}
	var got cliout.Envelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Meta.Command != "x.y" {
		t.Errorf("decoded meta.command = %q", got.Meta.Command)
	}
}

// TestErrorEnvelopeShape pins the error envelope:
//
//	{"error": {"code": "AGENT_NOT_FOUND", "message": "..."}}
//
// Codes are stable identifiers; messages are human and may change.
func TestErrorEnvelopeShape(t *testing.T) {
	env := cliout.ErrorEnvelope{
		Error: cliout.ErrorInfo{
			Code:    cliout.CodeAgentNotFound,
			Message: "no agent with id xyz",
		},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["error"]; !ok {
		t.Errorf("envelope must have an 'error' field; got %s", raw)
	}
	var got cliout.ErrorInfo
	if err := json.Unmarshal(generic["error"], &got); err != nil {
		t.Fatalf("unmarshal error info: %v", err)
	}
	if got.Code != "AGENT_NOT_FOUND" {
		t.Errorf("code = %q, want AGENT_NOT_FOUND", got.Code)
	}
	if got.Message == "" {
		t.Errorf("message must be non-empty")
	}
}

// TestStableCodes pins the documented stable error codes so a rename
// breaks a test instead of silently breaking downstream scripts.
func TestStableCodes(t *testing.T) {
	cases := map[string]string{
		"AGENT_NOT_FOUND":     cliout.CodeAgentNotFound,
		"DAEMON_UNREACHABLE":  cliout.CodeDaemonUnreachable,
		"INVALID_REF":         cliout.CodeInvalidRef,
		"RPC_TIMEOUT":         cliout.CodeRPCTimeout,
		"USAGE_ERROR":         cliout.CodeUsageError,
		"NO_RESULTS":          cliout.CodeNoResults,
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("code constant for %q drifted to %q", want, got)
		}
	}
}

// TestWriteErrorEnvelope checks the helper writes an envelope with a
// trailing newline so downstream NDJSON consumers can read line by line.
func TestWriteErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	err := cliout.WriteErrorEnvelope(&buf, cliout.CodeDaemonUnreachable, "daemon: not running")
	if err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	if buf.Bytes()[buf.Len()-1] != '\n' {
		t.Errorf("error envelope must end with newline; got %q", buf.String())
	}
	var got cliout.ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error.Code != "DAEMON_UNREACHABLE" {
		t.Errorf("code = %q", got.Error.Code)
	}
}

// TestVersionDefaultIsOne is a regression guard: nothing in v1 may
// change the default envelope version implicitly. A v2 bump requires
// a deliberate code change.
func TestVersionDefaultIsOne(t *testing.T) {
	root := &cobra.Command{Use: "sextant"}
	sub := &cobra.Command{Use: "agents"}
	root.AddCommand(sub)
	env := cliout.EnvelopeFromCommand(sub, nil)
	if env.Meta.Version != 1 {
		t.Errorf("default version = %d, want 1 — envelope v2 must be a deliberate bump", env.Meta.Version)
	}
}

// TestNoResultsErrorRoundTrip ensures the NO_RESULTS sentinel makes it
// through marshal/unmarshal as one of the stable codes.
func TestNoResultsErrorRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := cliout.WriteErrorEnvelope(&buf, cliout.CodeNoResults, "no audit rows"); err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	var got cliout.ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error.Code != cliout.CodeNoResults {
		t.Errorf("code = %q, want NO_RESULTS", got.Error.Code)
	}
}

// TestWriteEnvelopeNilData ensures an empty-but-present "data" field
// survives — empty array, not "null", so scripts can rely on jq
// `.data | length` returning 0 for an empty result.
func TestWriteEnvelopeEmptyData(t *testing.T) {
	var buf bytes.Buffer
	env := cliout.Envelope{
		Data: []string{},
		Meta: cliout.MetaInfo{Version: 1, Command: "pending.list"},
	}
	if err := cliout.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"data": []`)) {
		t.Errorf("empty data must serialize as [], not null; got %s", buf.String())
	}
}

// TestErrorsErrorString is a sanity check that ErrorEnvelope can be
// used as an error value via a small adapter (defensive for callers
// that want errors.As behavior).
func TestNewError(t *testing.T) {
	err := cliout.NewError(cliout.CodeInvalidRef, "agent: bad UUID")
	if err == nil {
		t.Fatal("NewError returned nil")
	}
	var got *cliout.CodedError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As must match *CodedError; err=%v", err)
	}
	if got.Code != cliout.CodeInvalidRef {
		t.Errorf("code = %q, want INVALID_REF", got.Code)
	}
}

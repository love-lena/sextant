// Package cliout owns the stable JSON output protocol that every
// `sextant <cmd> --json` site emits.
//
// The protocol is:
//
//	{"data": <payload>, "meta": {"version": 1, "command": "agents.list"}}
//
// for success, and
//
//	{"error": {"code": "AGENT_NOT_FOUND", "message": "..."}}
//
// for failure (written to stderr, paired with a non-zero exit code).
//
// Schema evolution rule: see doc.go. Additive changes don't bump the
// version; renames, removals, and enum reorderings bump
// `meta.version` and are gated on a `--meta-version=2` flag.
package cliout

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// EnvelopeVersion is the current envelope schema version. Any
// non-additive change (rename, removal, enum reordering) must bump
// this constant and ship a `--meta-version=2` flag. See doc.go for the
// full rule.
const EnvelopeVersion = 1

// Envelope is the wrapper that every successful `--json` emission
// produces. `Data` is the command-specific payload; `Meta` carries the
// version + dotted command path so downstream scripts can branch on
// schema drift.
type Envelope struct {
	Data any      `json:"data"`
	Meta MetaInfo `json:"meta"`
}

// MetaInfo carries the schema version and the dotted command path the
// envelope was produced by. The command name is canonicalized — see
// DottedCommand for the rule.
type MetaInfo struct {
	Version int    `json:"version"`
	Command string `json:"command"`
}

// ErrorEnvelope is the wrapper for failure responses. Emitted to
// stderr alongside a non-zero exit code.
type ErrorEnvelope struct {
	Error ErrorInfo `json:"error"`
}

// ErrorInfo names the failure with a stable, screaming-snake-case code
// (so scripts can branch on `.error.code`) and a human-readable
// message (which may change between releases).
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Stable error code constants. Adding new codes is fine; renaming or
// removing one breaks downstream scripts and requires a meta.version
// bump per doc.go.
const (
	CodeAgentNotFound     = "AGENT_NOT_FOUND"
	CodeDaemonUnreachable = "DAEMON_UNREACHABLE"
	CodeInvalidRef        = "INVALID_REF"
	CodeRPCTimeout        = "RPC_TIMEOUT"
	CodeUsageError        = "USAGE_ERROR"
	CodeNoResults         = "NO_RESULTS"
)

// EnvelopeFromCommand builds an envelope whose meta.command is derived
// from the cobra command's path (stripping the root "sextant" segment
// and joining with dots). `data` is wrapped verbatim.
func EnvelopeFromCommand(cmd *cobra.Command, data any) Envelope {
	dotted := ""
	if cmd != nil {
		dotted = DottedCommand(cmd.CommandPath())
	}
	return Envelope{
		Data: data,
		Meta: MetaInfo{
			Version: EnvelopeVersion,
			Command: dotted,
		},
	}
}

// DottedCommand canonicalizes a cobra CommandPath string into the
// dotted form used in `meta.command`. The leading "sextant" segment
// is stripped (it's implicit — every command starts with it). The
// rest are joined with dots.
//
//	"sextant agents list"   → "agents.list"
//	"sextant events tail"   → "events.tail"
//	"sextant init"          → "init"
//	"sextant"               → ""
func DottedCommand(path string) string {
	parts := strings.Fields(strings.TrimSpace(path))
	if len(parts) == 0 {
		return ""
	}
	// Drop the leading binary name ("sextant"). Defensive: only strip if
	// it actually matches, so DottedCommand("agents list") still works.
	if parts[0] == "sextant" {
		parts = parts[1:]
	}
	return strings.Join(parts, ".")
}

// WriteEnvelope writes a pretty-printed envelope to w with a trailing
// newline. Pretty-printing matches the existing `writeJSON` behavior
// in cmd/sextant so the diff stays minimal for human readers.
func WriteEnvelope(w io.Writer, env Envelope) error {
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// WriteErrorEnvelope writes a pretty-printed error envelope to w with
// a trailing newline. Use this on the stderr path when --json is set.
func WriteErrorEnvelope(w io.Writer, code, message string) error {
	env := ErrorEnvelope{Error: ErrorInfo{Code: code, Message: message}}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal error envelope: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// CodedError is the typed error sentinel callers use when they want to
// short-circuit a command with a specific stable code (and a
// human-readable message). The CLI's exit-code dispatcher uses
// errors.As(*CodedError) to recognize it and emit the error envelope.
type CodedError struct {
	Code    string
	Message string
}

func (e *CodedError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// NewError builds a *CodedError. Use the package's stable code
// constants for `code`.
func NewError(code, message string) error {
	return &CodedError{Code: code, Message: message}
}

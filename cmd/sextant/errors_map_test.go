package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/cliout"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fangStylesZero returns the zero-value fang.Styles for tests that
// don't care about styling (the errorBanner ignores its styles arg).
func fangStylesZero() fang.Styles { return fang.Styles{} }

// TestMapErrorToCodeTable pins every documented mapping from a CLI-side
// error to a stable cliout code. Each entry corresponds to a known
// failure shape the operator can hit with `--json`; adding a new
// known-shape error means adding a row here.
func TestMapErrorToCodeTable(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"no_results", errNoResults, cliout.CodeNoResults},
		{"usage_error", errUserUsage("bad flag"), cliout.CodeUsageError},
		{"client_rpc_timeout", client.ErrRPCTimeout, cliout.CodeRPCTimeout},
		{
			"rpc_agent_not_found",
			&client.RPCError{Code: sextantproto.ErrCodeAgentNotFound, Message: "no such agent"},
			cliout.CodeAgentNotFound,
		},
		{
			"rpc_agent_not_reachable",
			&client.RPCError{Code: sextantproto.ErrCodeAgentNotReachable, Message: "lifecycle=ended"},
			"AGENT_NOT_REACHABLE",
		},
		{
			"rpc_server_timeout",
			&client.RPCError{Code: sextantproto.ErrCodeTimeout, Message: "server timeout"},
			cliout.CodeRPCTimeout,
		},
		{
			"rpc_bad_request",
			&client.RPCError{Code: sextantproto.ErrCodeBadRequest, Message: "missing field"},
			cliout.CodeUsageError,
		},
		{
			"rpc_not_found_generic",
			&client.RPCError{Code: sextantproto.ErrCodeNotFound, Message: "no such worktree"},
			cliout.CodeAgentNotFound,
		},
		{
			"rpc_other_code",
			&client.RPCError{Code: sextantproto.ErrCodeCapabilityDenied, Message: "denied"},
			"RPC_ERROR",
		},
		{
			"daemon_unreachable_runtime_json",
			errors.New("read runtime.json: open /…/runtime.json: no such file or directory"),
			cliout.CodeDaemonUnreachable,
		},
		{
			"daemon_unreachable_connection_refused",
			errors.New("dial tcp 127.0.0.1:4222: connect: connection refused"),
			cliout.CodeDaemonUnreachable,
		},
		{
			"daemon_unreachable_sextantd_hint",
			fmt.Errorf("read runtime.json: %w (is sextantd running?)", errors.New("no such file")),
			cliout.CodeDaemonUnreachable,
		},
		{
			"default_catchall",
			errors.New("some unexpected condition"),
			"INTERNAL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCode, gotMsg := mapErrorToCode(tc.err)
			if gotCode != tc.wantCode {
				t.Errorf("code = %q, want %q (err: %v)", gotCode, tc.wantCode, tc.err)
			}
			if !strings.Contains(gotMsg, tc.err.Error()) {
				t.Errorf("message %q should contain the original err text %q", gotMsg, tc.err.Error())
			}
		})
	}
}

// TestMapErrorToCodeNilErr — defensive: nil err returns empty strings.
func TestMapErrorToCodeNilErr(t *testing.T) {
	code, msg := mapErrorToCode(nil)
	if code != "" || msg != "" {
		t.Errorf("nil err returned (%q, %q), want (\"\", \"\")", code, msg)
	}
}

// TestMapErrorToCodeWrappedSentinel — wrapped sentinels still match
// because mapErrorToCode uses errors.Is.
func TestMapErrorToCodeWrappedSentinel(t *testing.T) {
	wrapped := fmt.Errorf("verb: %w", errNoResults)
	if code, _ := mapErrorToCode(wrapped); code != cliout.CodeNoResults {
		t.Errorf("wrapped errNoResults code = %q, want %q", code, cliout.CodeNoResults)
	}
}

// TestErrorBannerJSONMode pins the actual wiring: with
// globalFlags.asJSON set, errorBanner emits the cliout error envelope
// instead of the plain `sextant: <err>` line. Catches regressions if
// someone shortcuts the asJSON check.
func TestErrorBannerJSONMode(t *testing.T) {
	prev := globalFlags.asJSON
	globalFlags.asJSON = true
	defer func() { globalFlags.asJSON = prev }()

	var buf strings.Builder
	errorBanner(&buf, fangStylesZero(), &client.RPCError{
		Code: sextantproto.ErrCodeAgentNotFound, Message: "no such agent",
	})

	got := buf.String()
	for _, want := range []string{`"error"`, `"code"`, "AGENT_NOT_FOUND", "no such agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON banner missing %q:\n%s", want, got)
		}
	}
}

// TestErrorBannerJSONModeEmitsForNoResults pins Codex's second-round
// finding: errNoResults must produce a NO_RESULTS error envelope under
// --json, not get silently dropped by the text-mode suppression rule
// that exists for the "no agents" stdout-line convention. Without
// this, `sextant agents list --json` against an empty fleet exits 10
// with no machine-readable failure object — a broken contract for
// scripts.
func TestErrorBannerJSONModeEmitsForNoResults(t *testing.T) {
	prev := globalFlags.asJSON
	globalFlags.asJSON = true
	defer func() { globalFlags.asJSON = prev }()

	var buf strings.Builder
	errorBanner(&buf, fangStylesZero(), errNoResults)

	got := buf.String()
	for _, want := range []string{`"error"`, `"code"`, cliout.CodeNoResults} {
		if !strings.Contains(got, want) {
			t.Errorf("no-results JSON banner missing %q:\n%s", want, got)
		}
	}
}

// TestErrorBannerPlainModeSuppressesNoResults confirms the
// suppression rule still applies in TEXT mode — errNoResults is
// silenced because the verb already wrote "no agents" / "no pending
// requests" to stdout. Without this guard the operator would see a
// noisy `sextant: no results` line alongside the verb's own message.
func TestErrorBannerPlainModeSuppressesNoResults(t *testing.T) {
	prev := globalFlags.asJSON
	globalFlags.asJSON = false
	defer func() { globalFlags.asJSON = prev }()

	var buf strings.Builder
	errorBanner(&buf, fangStylesZero(), errNoResults)

	if got := buf.String(); got != "" {
		t.Errorf("text-mode errNoResults should be silent, got %q", got)
	}
}

// TestErrorBannerPlainMode confirms the default path still writes the
// pre-cobra plain-text banner. The text mode is the operator's
// default — don't accidentally JSON-ify it.
func TestErrorBannerPlainMode(t *testing.T) {
	prev := globalFlags.asJSON
	globalFlags.asJSON = false
	defer func() { globalFlags.asJSON = prev }()

	var buf strings.Builder
	errorBanner(&buf, fangStylesZero(), errors.New("boom"))

	got := buf.String()
	if !strings.HasPrefix(got, "sextant: ") {
		t.Errorf("plain banner = %q, want prefix \"sextant: \"", got)
	}
	if strings.Contains(got, `"error":`) {
		t.Errorf("plain banner unexpectedly emitted JSON: %q", got)
	}
}

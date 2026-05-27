// errors_map.go owns the mapping from CLI-side errors to the stable
// cliout error codes that `sextant <verb> --json` emits on stderr.
//
// Per feat-cli-output-protocol-tail-and-errors and the codex
// adversarial-review finding 2: without this mapping, `--json`
// failures fell through to fang's plain-text banner and violated the
// machine-readable protocol downstream scripts depend on.
//
// New mappings land here when:
//   - a new sentinel error gets added in cmd/sextant/
//   - a new sextantproto.ErrCode* surfaces from the daemon
//   - a new cliout.Code* constant is added
package main

import (
	"errors"
	"strings"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/cliout"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// mapErrorToCode classifies err into a stable cliout error code +
// operator-facing message. The message is the original err string —
// human-readable diagnostics survive even when the code is the
// catchall.
//
// Mapping (in priority order, first match wins):
//
//	errNoResults                       → cliout.CodeNoResults
//	usageError                         → cliout.CodeUsageError
//	client.ErrRPCTimeout               → cliout.CodeRPCTimeout
//	*client.RPCError with Code:
//	  ErrCodeAgentNotFound             → cliout.CodeAgentNotFound
//	  ErrCodeAgentNotReachable         → "AGENT_NOT_REACHABLE"
//	  ErrCodeTimeout                   → cliout.CodeRPCTimeout
//	  ErrCodeBadRequest                → cliout.CodeUsageError
//	  ErrCodeNotFound                  → cliout.CodeAgentNotFound (closest fit)
//	  anything else                    → "RPC_ERROR"
//	error string matches "is sextantd running" / "connect:" / "dial " → cliout.CodeDaemonUnreachable
//	default                            → "INTERNAL"
//
// The "RPC_ERROR" and "AGENT_NOT_REACHABLE" codes don't have
// cliout.Code* constants today; they're inlined here so scripts can
// still branch on the well-known string. Promote them to constants if
// they grow more call sites.
func mapErrorToCode(err error) (code, message string) {
	if err == nil {
		return "", ""
	}
	message = err.Error()

	if errors.Is(err, errNoResults) {
		return cliout.CodeNoResults, message
	}
	var ue usageError
	if errors.As(err, &ue) {
		return cliout.CodeUsageError, message
	}
	if errors.Is(err, client.ErrRPCTimeout) {
		return cliout.CodeRPCTimeout, message
	}

	var rpcErr *client.RPCError
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case sextantproto.ErrCodeAgentNotFound:
			return cliout.CodeAgentNotFound, message
		case sextantproto.ErrCodeAgentNotReachable:
			return "AGENT_NOT_REACHABLE", message
		case sextantproto.ErrCodeTimeout:
			return cliout.CodeRPCTimeout, message
		case sextantproto.ErrCodeBadRequest:
			return cliout.CodeUsageError, message
		case sextantproto.ErrCodeNotFound:
			return cliout.CodeAgentNotFound, message
		}
		return "RPC_ERROR", message
	}

	// Daemon-unreachable detection. The connect path returns wrapped
	// errors that contain hints like "is sextantd running?" or raw
	// dial errors. Match on substring as a fallback — operators just
	// need the code to be DAEMON_UNREACHABLE so the CLI / script can
	// pivot to `sextant daemon start`.
	low := strings.ToLower(message)
	switch {
	case strings.Contains(low, "is sextantd running"),
		strings.Contains(low, "read runtime.json"),
		strings.Contains(low, "connection refused"),
		strings.Contains(low, "no such file or directory") && strings.Contains(low, "runtime.json"):
		return cliout.CodeDaemonUnreachable, message
	}

	return "INTERNAL", message
}

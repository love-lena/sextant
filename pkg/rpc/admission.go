package rpc

import (
	"fmt"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Admission is the shared decode → default → validate pre-step that runs
// in front of EVERY RPC handler (control-plane RFC §5.7). It is the one
// choke point so no handler can skip envelope validation, the wire-epoch
// compatibility check, or (later) audit — the front-door guarantee made
// structural rather than re-inlined per handler and drifting between
// verbs.
//
// The envelope is already JSON-decoded by the dispatcher (the "decode"
// step). Admit performs the remaining two steps, mutating-then-validating:
//
//  1. default — fill envelope fields the wire contract lets a client omit
//     (proto_version) so downstream code sees a normalized envelope.
//  2. validate — structural envelope validation (sextantproto.Validate)
//     PLUS the wire-epoch check: reject a stale-epoch peer with an
//     actionable diagnostic.
//
// Admit returns the (possibly defaulted) envelope and, on rejection, a
// terminal RPCError the dispatcher publishes verbatim. A nil error means
// the request is admitted.
func Admit(req sextantproto.Envelope) (sextantproto.Envelope, *sextantproto.RPCError) {
	// --- default ---------------------------------------------------------
	// A client that forgot proto_version is treated as same-epoch (the
	// envelope still has to pass Validate, which requires a non-empty
	// proto_version, so an empty value is normalized to the daemon's).
	if req.ProtoVersion == "" {
		req.ProtoVersion = sextantproto.ProtoVersion
	}

	// --- validate (structural) ------------------------------------------
	if err := req.Validate(); err != nil {
		return req, &sextantproto.RPCError{
			Code:    sextantproto.ErrCodeBadRequest,
			Message: fmt.Sprintf("envelope failed validation: %v", err),
		}
	}

	// --- validate (wire epoch) ------------------------------------------
	// proto_version is the wire-carried compatibility token: it is
	// generated from the same source as WireEpoch and bumps in lockstep
	// (sextantproto/doc.go), so a proto_version that differs from the
	// daemon's is, by construction, a different wire epoch. The daemon
	// can converge an out-of-epoch *agent* by restart, but it cannot
	// restart the operator's CLI — so a stale peer fails fast here with
	// the reinstall remedy (RFC §5.8 "stale peer").
	if req.ProtoVersion != sextantproto.ProtoVersion {
		return req, &sextantproto.RPCError{
			Code: sextantproto.ErrCodeWireEpochMismatch,
			Message: fmt.Sprintf(
				"client wire epoch is incompatible (client proto_version=%s, daemon proto_version=%s, daemon wire_epoch=%d); "+
					"reinstall the sextant CLI to match the running daemon: make install",
				req.ProtoVersion, sextantproto.ProtoVersion, sextantproto.WireEpoch,
			),
			Details: map[string]any{
				"client_proto_version": req.ProtoVersion,
				"daemon_proto_version": sextantproto.ProtoVersion,
				"daemon_wire_epoch":    sextantproto.WireEpoch,
				"remedy":               "make install",
			},
		}
	}

	return req, nil
}

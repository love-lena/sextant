package rpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestAdmitAcceptsCurrentEpoch confirms a well-formed envelope on the
// daemon's own proto_version is admitted unchanged.
func TestAdmitAcceptsCurrentEpoch(t *testing.T) {
	env := sextantproto.NewEnvelope(
		sextantproto.KindRPCRequest,
		sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"},
		json.RawMessage(`{}`),
	)
	out, rerr := Admit(env)
	if rerr != nil {
		t.Fatalf("Admit rejected a current-epoch envelope: %+v", rerr)
	}
	if out.ProtoVersion != sextantproto.ProtoVersion {
		t.Errorf("proto_version = %q, want %q", out.ProtoVersion, sextantproto.ProtoVersion)
	}
}

// TestAdmitDefaultsMissingProtoVersion confirms the "default" step fills
// an omitted proto_version with the daemon's (treated as same-epoch).
func TestAdmitDefaultsMissingProtoVersion(t *testing.T) {
	env := sextantproto.NewEnvelope(
		sextantproto.KindRPCRequest,
		sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"},
		json.RawMessage(`{}`),
	)
	env.ProtoVersion = ""
	out, rerr := Admit(env)
	if rerr != nil {
		t.Fatalf("Admit rejected an empty-proto_version envelope: %+v", rerr)
	}
	if out.ProtoVersion != sextantproto.ProtoVersion {
		t.Errorf("proto_version not defaulted: got %q, want %q", out.ProtoVersion, sextantproto.ProtoVersion)
	}
}

// TestAdmitRejectsStaleEpoch is the WireEpoch acceptance check: a peer on
// a different wire epoch (proto_version) is rejected with the
// wire_epoch_mismatch code and an actionable `make install` diagnostic.
func TestAdmitRejectsStaleEpoch(t *testing.T) {
	env := sextantproto.NewEnvelope(
		sextantproto.KindRPCRequest,
		sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"},
		json.RawMessage(`{}`),
	)
	env.ProtoVersion = "0.0.1-ancient"
	_, rerr := Admit(env)
	if rerr == nil {
		t.Fatal("Admit accepted a stale-epoch envelope; the front door is open")
	}
	if rerr.Code != sextantproto.ErrCodeWireEpochMismatch {
		t.Errorf("code = %q, want %q", rerr.Code, sextantproto.ErrCodeWireEpochMismatch)
	}
	if !strings.Contains(rerr.Message, "make install") {
		t.Errorf("message lacks the reinstall remedy: %q", rerr.Message)
	}
	if rerr.Details == nil {
		t.Fatal("rejection carries no Details")
	}
	if got := rerr.Details["client_proto_version"]; got != "0.0.1-ancient" {
		t.Errorf("details.client_proto_version = %v, want 0.0.1-ancient", got)
	}
	if got := rerr.Details["daemon_wire_epoch"]; got != sextantproto.WireEpoch {
		t.Errorf("details.daemon_wire_epoch = %v, want %d", got, sextantproto.WireEpoch)
	}
	if got := rerr.Details["remedy"]; got != "make install" {
		t.Errorf("details.remedy = %v, want \"make install\"", got)
	}
}

// TestAdmitRejectsMalformedEnvelope confirms the structural validation
// step still fires (e.g. a nil trace id) before the epoch check.
func TestAdmitRejectsMalformedEnvelope(t *testing.T) {
	var env sextantproto.Envelope // zero value: nil ids, empty kind
	env.ProtoVersion = sextantproto.ProtoVersion
	_, rerr := Admit(env)
	if rerr == nil {
		t.Fatal("Admit accepted a structurally invalid envelope")
	}
	if rerr.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("code = %q, want %q", rerr.Code, sextantproto.ErrCodeBadRequest)
	}
}

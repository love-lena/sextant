package sextantproto

import (
	"encoding/json"
	"testing"
)

func TestRPCRequestRoundTrip(t *testing.T) {
	req := RPCRequest{
		Verb: "list_agents",
		Args: json.RawMessage(`{"filter":{}}`),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RPCRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Verb != req.Verb || string(back.Args) != string(req.Args) {
		t.Fatalf("rpc request roundtrip mismatch")
	}
}

func TestRPCResponseSuccessAndError(t *testing.T) {
	ok := RPCResponse{
		Result:   json.RawMessage(`{"agents":[]}`),
		Terminal: true,
	}
	rawOK, _ := json.Marshal(ok)
	var backOK RPCResponse
	if err := json.Unmarshal(rawOK, &backOK); err != nil {
		t.Fatalf("ok unmarshal: %v", err)
	}
	if !backOK.Terminal || backOK.Error != nil {
		t.Fatalf("success response roundtrip mismatch")
	}

	rerr := RPCResponse{
		Error: &RPCError{
			Code:    ErrCodeAgentNotFound,
			Message: "no such agent",
			Details: map[string]any{"agent_id": "abc"},
		},
		Terminal: true,
	}
	rawErr, _ := json.Marshal(rerr)
	var backErr RPCResponse
	if err := json.Unmarshal(rawErr, &backErr); err != nil {
		t.Fatalf("err unmarshal: %v", err)
	}
	if backErr.Error == nil {
		t.Fatal("error should be present")
	}
	if backErr.Error.Code != ErrCodeAgentNotFound {
		t.Fatalf("error code roundtrip mismatch: %s", backErr.Error.Code)
	}
}

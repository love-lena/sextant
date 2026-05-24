package sextantproto

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestAgentFramePayloadRoundTrip(t *testing.T) {
	body := AgentFramePayload{
		FrameKind: FrameToolCall,
		SessionID: "sess-1",
		ToolName:  "read_file",
		Body:      map[string]any{"path": "README.md", "lines": 10.0},
		Tokens:    &FrameTokens{Input: 1000, Output: 200, CacheRead: 50},
		Tags:      map[string]string{"agent": "lead"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AgentFramePayload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.FrameKind != FrameToolCall || back.ToolName != "read_file" {
		t.Fatalf("payload roundtrip mismatch")
	}
	if back.Tokens == nil || back.Tokens.Input != 1000 {
		t.Fatalf("tokens roundtrip mismatch")
	}
	if back.Body["path"] != "README.md" {
		t.Fatalf("body roundtrip mismatch")
	}
}

func TestLifecyclePayloadRoundTrip(t *testing.T) {
	p := LifecyclePayload{
		IncarnationID: uuid.New(),
		AgentUUID:     uuid.New(),
		Transition:    LifecycleStarted,
		State:         IncarnationReady,
		Reason:        "spawned by lead",
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back LifecyclePayload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Transition != LifecycleStarted || back.State != IncarnationReady {
		t.Fatalf("lifecycle roundtrip mismatch")
	}
	if back.AgentUUID != p.AgentUUID || back.IncarnationID != p.IncarnationID {
		t.Fatalf("ids roundtrip mismatch")
	}
}

func TestAuditPayloadRoundTrip(t *testing.T) {
	id := uuid.New()
	p := AuditPayload{
		Actor:              "lena",
		AgentUUID:          &id,
		Action:             "spawn_agent",
		CapabilityRequired: "control.spawn",
		Result:             AuditAllowed,
		Details:            map[string]any{"template": "default"},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AuditPayload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Action != "spawn_agent" || back.Result != AuditAllowed {
		t.Fatalf("audit roundtrip mismatch")
	}
	if back.AgentUUID == nil || *back.AgentUUID != id {
		t.Fatalf("agent uuid roundtrip mismatch")
	}
}

func TestUserInputRoundTrip(t *testing.T) {
	req := UserInputRequestPayload{
		RequestID: uuid.New(),
		FromUUID:  uuid.New(),
		Question:  "Which library?",
		Options:   []string{"a", "b"},
		Urgency:   "high",
	}
	raw, _ := json.Marshal(req)
	var backReq UserInputRequestPayload
	if err := json.Unmarshal(raw, &backReq); err != nil {
		t.Fatalf("req unmarshal: %v", err)
	}
	if backReq.Question != req.Question || len(backReq.Options) != 2 {
		t.Fatalf("req roundtrip mismatch")
	}

	resp := UserInputResponsePayload{
		RequestID: req.RequestID,
		Decision:  InputAnswer,
		Answer:    "a",
	}
	rraw, _ := json.Marshal(resp)
	var backResp UserInputResponsePayload
	if err := json.Unmarshal(rraw, &backResp); err != nil {
		t.Fatalf("resp unmarshal: %v", err)
	}
	if backResp.Decision != InputAnswer || backResp.Answer != "a" {
		t.Fatalf("resp roundtrip mismatch")
	}
}

func TestHeartbeatRoundTrip(t *testing.T) {
	ts := NowTimestamp()
	h := HeartbeatPayload{
		AgentUUID:      uuid.New(),
		IncarnationID:  uuid.New(),
		HostID:         "host-a",
		UptimeSeconds:  120,
		LastFrameTs:    &ts,
		PendingPrompts: 3,
		ResourceUsage:  map[string]string{"cpu_pct": "12.5"},
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back HeartbeatPayload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.UptimeSeconds != 120 || back.PendingPrompts != 3 {
		t.Fatalf("heartbeat roundtrip mismatch")
	}
	if back.LastFrameTs == nil || !back.LastFrameTs.Equal(ts.Time) {
		t.Fatalf("last frame ts roundtrip mismatch")
	}
}

package sextantproto

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestAgentDefinitionRoundTrip(t *testing.T) {
	host := "host-a"
	sess := "sess-123"
	def := AgentDefinition{
		UUID:     uuid.New(),
		Name:     "lead",
		Type:     "lead",
		Template: "lead.toml",
		Runtime: RuntimeConfig{
			Model:          "claude-opus-4-7[1m]",
			Provider:       "anthropic",
			Params:         map[string]string{"max_tokens": "8192"},
			PermissionMode: "auto",
			SessionID:      &sess,
			PermissionCeil: "auto",
		},
		Sandbox: SandboxConfig{
			Image:        "sextant-sidecar:0.1",
			Mounts:       []string{"worktree"},
			Env:          map[string]string{"FOO": "bar"},
			ResourceLims: ResourceLimits{CPUShares: 1024, MemoryMiB: 2048},
		},
		Tools:     []string{"send_message", "list_agents"},
		HostPin:   &host,
		Lifecycle: LifecycleRunning,
		Version:   3,
		CreatedAt: NowTimestamp(),
		UpdatedAt: NowTimestamp(),
	}

	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AgentDefinition
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.UUID != def.UUID {
		t.Fatalf("uuid roundtrip mismatch")
	}
	if back.Name != def.Name || back.Lifecycle != def.Lifecycle {
		t.Fatalf("name/lifecycle roundtrip mismatch")
	}
	if back.Runtime.SessionID == nil || *back.Runtime.SessionID != sess {
		t.Fatalf("session_id roundtrip mismatch")
	}
	if back.Sandbox.ResourceLims.MemoryMiB != def.Sandbox.ResourceLims.MemoryMiB {
		t.Fatalf("resource limit roundtrip mismatch")
	}
}

func TestAgentIncarnationRoundTrip(t *testing.T) {
	exit := 0
	end := NowTimestamp()
	inc := AgentIncarnation{
		IncarnationID: uuid.New(),
		AgentUUID:     uuid.New(),
		StartedAt:     NowTimestamp(),
		EndedAt:       &end,
		HostID:        "host-a",
		ContainerID:   "abcd1234",
		State:         IncarnationExited,
		ExitCode:      &exit,
		JWTKeyID:      "k1",
	}
	raw, err := json.Marshal(inc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AgentIncarnation
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.State != IncarnationExited || back.ContainerID != inc.ContainerID {
		t.Fatalf("incarnation roundtrip mismatch")
	}
	if back.ExitCode == nil || *back.ExitCode != exit {
		t.Fatalf("exit code roundtrip mismatch")
	}
}

func TestLifecycleStateValidation(t *testing.T) {
	for _, s := range []LifecycleState{LifecycleDefined, LifecycleRunning, LifecyclePaused, LifecycleArchived} {
		if !s.IsValid() {
			t.Fatalf("%q should be valid", s)
		}
	}
	if LifecycleState("running-fast").IsValid() {
		t.Fatal("garbage state should not be valid")
	}
}

func TestIncarnationStateValidation(t *testing.T) {
	for _, s := range []IncarnationState{IncarnationStarting, IncarnationReady, IncarnationExited, IncarnationFailed} {
		if !s.IsValid() {
			t.Fatalf("%q should be valid", s)
		}
	}
	if IncarnationState("zombie").IsValid() {
		t.Fatal("garbage state should not be valid")
	}
}

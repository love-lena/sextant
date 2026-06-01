package sextantproto

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestAgentDefinitionRoundTrip(t *testing.T) {
	host := "host-a"
	sess := "sess-123"
	inc := uuid.New()
	// Post spec/status split (RFC §5.2, Appendix C): the immutable-ish
	// shape lives under Spec; observed truth lives under Status.
	def := AgentDefinition{
		UUID:     uuid.New(),
		Name:     "lead",
		Type:     "lead",
		Template: "lead.toml",
		Spec: AgentSpec{
			Desired: DesiredRun,
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
			Tools:            []string{"send_message", "list_agents"},
			HostPin:          &host,
			RestartPolicy:    RestartOnFailure,
			Generation:       2,
			ReactuationNonce: 1,
		},
		Status: AgentStatusRecord{
			Observed:             ObservedRunning,
			Phase:                string(ObservedRunning),
			CurrentIncarnationID: inc,
			ObservedGeneration:   2,
			ObservedNonce:        1,
			RestartCount:         1,
		},
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
	if back.Name != def.Name {
		t.Fatalf("name roundtrip mismatch")
	}
	if back.Spec.Desired != def.Spec.Desired {
		t.Fatalf("spec.desired roundtrip mismatch: got %q want %q", back.Spec.Desired, def.Spec.Desired)
	}
	if back.Status.Observed != def.Status.Observed {
		t.Fatalf("status.observed roundtrip mismatch: got %q want %q", back.Status.Observed, def.Status.Observed)
	}
	if back.Status.CurrentIncarnationID != inc {
		t.Fatalf("status.current_incarnation_id roundtrip mismatch")
	}
	if back.Spec.Generation != def.Spec.Generation || back.Status.ObservedGeneration != def.Status.ObservedGeneration {
		t.Fatalf("generation roundtrip mismatch")
	}
	if back.Spec.ReactuationNonce != def.Spec.ReactuationNonce || back.Status.ObservedNonce != def.Status.ObservedNonce {
		t.Fatalf("nonce roundtrip mismatch")
	}
	// Lifecycle() is now a derived projection of the spec/status split.
	if back.Lifecycle() != LifecycleRunning {
		t.Fatalf("Lifecycle() projection = %q, want running", back.Lifecycle())
	}
	if back.Spec.Runtime.SessionID == nil || *back.Spec.Runtime.SessionID != sess {
		t.Fatalf("session_id roundtrip mismatch")
	}
	if back.Spec.Sandbox.ResourceLims.MemoryMiB != def.Spec.Sandbox.ResourceLims.MemoryMiB {
		t.Fatalf("resource limit roundtrip mismatch")
	}
}

// TestLifecycleProjection asserts the derived Lifecycle() rollup over the
// spec/status split (RFC §5.2): desired intent wins for paused/archived;
// otherwise the observed value (or "defined" when pending pre-actuation).
func TestLifecycleProjection(t *testing.T) {
	cases := []struct {
		name     string
		desired  DesiredState
		observed ObservedState
		want     LifecycleState
	}{
		{"archived intent wins", DesiredArchived, ObservedRunning, LifecycleArchived},
		{"paused intent wins", DesiredPaused, ObservedRunning, LifecyclePaused},
		{"run+running", DesiredRun, ObservedRunning, LifecycleRunning},
		{"run+crashed", DesiredRun, ObservedCrashed, LifecycleCrashedState},
		{"run+lost", DesiredRun, ObservedLost, LifecycleLostState},
		{"run+ended", DesiredRun, ObservedEnded, LifecycleEndedState},
		{"run+pending -> defined", DesiredRun, ObservedPending, LifecycleDefined},
		{"run+unset -> defined", DesiredRun, "", LifecycleDefined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := AgentDefinition{
				Spec:   AgentSpec{Desired: tc.desired},
				Status: AgentStatusRecord{Observed: tc.observed},
			}
			if got := d.Lifecycle(); got != tc.want {
				t.Fatalf("Lifecycle() = %q, want %q", got, tc.want)
			}
		})
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

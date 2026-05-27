package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/cliout"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fakeAgentChecker drives runAgentCheck for unit tests without a
// live daemon. ResolveErr / StatusErr let each test inject a failure.
type fakeAgentChecker struct {
	ID         uuid.UUID
	Status     sextantproto.AgentStatus
	ResolveErr error
	StatusErr  error
}

func (f *fakeAgentChecker) ResolveAgentRef(_ context.Context, _ string) (uuid.UUID, error) {
	if f.ResolveErr != nil {
		return uuid.Nil, f.ResolveErr
	}
	return f.ID, nil
}

func (f *fakeAgentChecker) GetAgentStatus(_ context.Context, _ uuid.UUID) (sextantproto.AgentStatus, error) {
	if f.StatusErr != nil {
		return sextantproto.AgentStatus{}, f.StatusErr
	}
	return f.Status, nil
}

// TestRunAgentCheckHealthy — running lifecycle → verdict=healthy, no
// remedy command (operator has nothing to do).
func TestRunAgentCheckHealthy(t *testing.T) {
	id := uuid.New()
	ch := &fakeAgentChecker{
		ID: id,
		Status: sextantproto.AgentStatus{
			UUID:      id,
			Name:      "alpha",
			Lifecycle: string(sextantproto.LifecycleRunning),
			Version:   3,
			UpdatedAt: time.Now(),
		},
	}
	got := runAgentCheck(context.Background(), ch, "alpha")
	if got.Verdict != "healthy" {
		t.Errorf("Verdict = %q, want healthy", got.Verdict)
	}
	if got.Remedy != "" {
		t.Errorf("Remedy = %q on healthy agent, want empty", got.Remedy)
	}
}

// TestRunAgentCheckTerminalLifecycle — covers the verdicts that print
// a remedy command. Mirrors the ticket's TestAgentsCheckEnded /
// TestAgentsCheckPaused / TestAgentsCheckArchived shape.
func TestRunAgentCheckTerminalLifecycle(t *testing.T) {
	cases := []struct {
		name        string
		lifecycle   sextantproto.LifecycleState
		wantVerdict string
		wantRemedy  string
	}{
		{"ended", sextantproto.LifecycleEndedState, "ended", "sextant agents restart"},
		{"crashed", sextantproto.LifecycleCrashedState, "ended", "sextant agents restart"},
		{"paused", sextantproto.LifecyclePaused, "paused", "sextant agents restart"},
		{"archived", sextantproto.LifecycleArchived, "archived", "spawn a new agent"},
		{"defined", sextantproto.LifecycleDefined, "stale_record", "sextant agents restart"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			ch := &fakeAgentChecker{
				ID: id,
				Status: sextantproto.AgentStatus{
					UUID:      id,
					Name:      "beta",
					Lifecycle: string(tc.lifecycle),
				},
			}
			got := runAgentCheck(context.Background(), ch, "beta")
			if got.Verdict != tc.wantVerdict {
				t.Errorf("Verdict = %q, want %q", got.Verdict, tc.wantVerdict)
			}
			if !strings.Contains(got.Remedy, tc.wantRemedy) {
				t.Errorf("Remedy = %q, want substring %q", got.Remedy, tc.wantRemedy)
			}
		})
	}
}

// TestRunAgentCheckNotFound — resolve failure → verdict=not_found.
func TestRunAgentCheckNotFound(t *testing.T) {
	ch := &fakeAgentChecker{ResolveErr: errors.New("no agent with name gamma")}
	got := runAgentCheck(context.Background(), ch, "gamma")
	if got.Verdict != "not_found" {
		t.Errorf("Verdict = %q, want not_found", got.Verdict)
	}
}

// TestRunAgentCheckRPCError — resolve succeeds but get_agent_status
// fails (daemon unreachable, transient error) → verdict=rpc_error,
// remedy points at `sextant doctor`.
func TestRunAgentCheckRPCError(t *testing.T) {
	ch := &fakeAgentChecker{
		ID:        uuid.New(),
		StatusErr: errors.New("daemon unreachable"),
	}
	got := runAgentCheck(context.Background(), ch, "delta")
	if got.Verdict != "rpc_error" {
		t.Errorf("Verdict = %q, want rpc_error", got.Verdict)
	}
	if !strings.Contains(got.Remedy, "sextant doctor") {
		t.Errorf("Remedy = %q, want sextant doctor mention", got.Remedy)
	}
}

// TestRenderAgentCheckJSONShape pins the --json schema so scripting
// consumers can rely on it.
func TestRenderAgentCheckJSONShape(t *testing.T) {
	id := uuid.New()
	check := AgentCheck{
		Ref:       "alpha",
		UUID:      id,
		Name:      "alpha",
		Lifecycle: "running",
		Version:   3,
		UpdatedAt: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
		Verdict:   "healthy",
	}
	var buf bytes.Buffer
	cmd := newAgentsCheckCmd()
	if err := renderAgentCheck(cmd, &buf, check, true); err != nil {
		t.Fatalf("render: %v", err)
	}
	var env cliout.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Meta.Version != cliout.EnvelopeVersion {
		t.Errorf("Meta.Version = %d, want %d", env.Meta.Version, cliout.EnvelopeVersion)
	}
	// Round-trip the envelope's data into AgentCheck via JSON since the
	// envelope's Data is typed `any`.
	dataRaw, _ := json.Marshal(env.Data)
	var got AgentCheck
	if err := json.Unmarshal(dataRaw, &got); err != nil {
		t.Fatalf("decode rendered JSON: %v", err)
	}
	if got.UUID != id || got.Verdict != "healthy" {
		t.Errorf("decoded check = %+v, want UUID=%s verdict=healthy", got, id)
	}
}

// TestAgentCheckToResult covers the projection of AgentCheck verdicts
// into the doctor CheckResult shape used by `doctor --agents`. The
// mapping decides which agents the table flags red vs yellow.
func TestAgentCheckToResult(t *testing.T) {
	cases := []struct {
		verdict    string
		wantStatus CheckStatus
	}{
		{"healthy", StatusPass},
		{"paused", StatusWarn},
		{"archived", StatusWarn},
		{"ended", StatusFail},
		{"stale_record", StatusFail},
		{"rpc_error", StatusFail},
		{"not_found", StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.verdict, func(t *testing.T) {
			got := agentCheckToResult("alpha", AgentCheck{
				Verdict:   tc.verdict,
				Lifecycle: "running",
				Remedy:    "x",
			})
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Kind != "agent" {
				t.Errorf("Kind = %q, want agent", got.Kind)
			}
			if got.Check != "alpha" {
				t.Errorf("Check = %q, want alpha", got.Check)
			}
		})
	}
}

// TestRenderAgentCheckTextShape pins the human render contract: each
// of the relevant fields lands on its own line, easy to grep.
func TestRenderAgentCheckTextShape(t *testing.T) {
	id := uuid.New()
	check := AgentCheck{
		Ref:       "alpha",
		UUID:      id,
		Name:      "alpha",
		Lifecycle: "ended",
		Verdict:   "ended",
		Remedy:    "sextant agents restart " + id.String(),
		UpdatedAt: time.Now(),
	}
	var buf bytes.Buffer
	cmd := newAgentsCheckCmd()
	if err := renderAgentCheck(cmd, &buf, check, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"agent:", "verdict: ended", "remedy:", "sextant agents restart"} {
		if !strings.Contains(got, want) {
			t.Errorf("text render missing %q:\n%s", want, got)
		}
	}
}

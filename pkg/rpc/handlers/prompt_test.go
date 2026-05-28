package handlers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fakeHeartbeats is a minimal HeartbeatLookup for handler tests.
type fakeHeartbeats struct {
	lastSeen map[uuid.UUID]time.Time
}

func (f *fakeHeartbeats) LastSeen(id uuid.UUID) (time.Time, bool) {
	t, ok := f.lastSeen[id]
	return t, ok
}

// TestPromptAgentRejectsTerminalLifecycle pins the acceptance test from
// bug-prompt-agent-accepts-when-sidecar-gone: when the agent's recorded
// lifecycle is one of the terminal states, prompt_agent must refuse with
// a structured error containing the remedy command, not silently publish
// into a void inbox. Combined with the lifecycle watcher
// (bug-agents-list-stale-lifecycle), this closes the "sidecar gone but
// prompt_agent returns ok=true" gap.
func TestPromptAgentRejectsTerminalLifecycle(t *testing.T) {
	cases := []struct {
		name       string
		lifecycle  sextantproto.LifecycleState
		wantRemedy string // substring expected in the error message
		wantCode   string
	}{
		{
			name:       "ended",
			lifecycle:  sextantproto.LifecycleEndedState,
			wantRemedy: "sextant agents restart",
			wantCode:   sextantproto.ErrCodeAgentNotReachable,
		},
		{
			name:       "crashed",
			lifecycle:  sextantproto.LifecycleCrashedState,
			wantRemedy: "sextant agents restart",
			wantCode:   sextantproto.ErrCodeAgentNotReachable,
		},
		{
			name:       "paused",
			lifecycle:  sextantproto.LifecyclePaused,
			wantRemedy: "sextant agents restart",
			wantCode:   sextantproto.ErrCodeAgentNotReachable,
		},
		{
			name:       "archived",
			lifecycle:  sextantproto.LifecycleArchived,
			wantRemedy: "spawn a new agent",
			wantCode:   sextantproto.ErrCodeAgentNotReachable,
		},
		{
			name:       "defined",
			lifecycle:  sextantproto.LifecycleDefined,
			wantRemedy: "sextant agents restart",
			wantCode:   sextantproto.ErrCodeAgentNotReachable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kv := newFakeMutableKV()
			id := uuid.New()
			seedAgentDefinition(t, kv, id, "alpha", tc.lifecycle)

			h := handlers.NewPromptAgent(handlers.PromptDeps{
				Definitions: kv,
				NATS:        nil, // not reached — handler refuses before publish
				From:        sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
			})
			cap := &captureEmit{}
			req := makeReq(t, sextantproto.PromptAgentRequest{
				AgentID: id,
				Content: "ping",
			})
			if err := h(context.Background(), req, cap.emit()); err != nil {
				t.Fatalf("handler: %v", err)
			}
			if cap.resp.Error == nil {
				t.Fatalf("expected error, got ok response: %+v", cap.resp)
			}
			if cap.resp.Error.Code != tc.wantCode {
				t.Errorf("Error.Code = %q, want %q", cap.resp.Error.Code, tc.wantCode)
			}
			if !strings.Contains(cap.resp.Error.Message, tc.wantRemedy) {
				t.Errorf("Error.Message = %q, want substring %q",
					cap.resp.Error.Message, tc.wantRemedy)
			}
		})
	}
}

// TestPromptAgentNotFound covers the unknown-agent path — distinct
// error code so CLI can render a different message than the
// not-reachable case.
func TestPromptAgentNotFound(t *testing.T) {
	kv := newFakeMutableKV()
	h := handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions: kv,
		NATS:        nil,
		From:        sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.PromptAgentRequest{
		AgentID: uuid.New(),
		Content: "ping",
	})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatalf("expected error, got ok: %+v", cap.resp)
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Errorf("Error.Code = %q, want %q",
			cap.resp.Error.Code, sextantproto.ErrCodeAgentNotFound)
	}
}

func seedAgentDefinition(t *testing.T, kv *fakeMutableKV, id uuid.UUID, name string, lifecycle sextantproto.LifecycleState) {
	t.Helper()
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      name,
		Type:      "assistant",
		Template:  "default",
		Lifecycle: lifecycle,
		Version:   1,
		CreatedAt: sextantproto.NowTimestamp(),
		UpdatedAt: sextantproto.NowTimestamp(),
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	if _, err := kv.Put(context.Background(), id.String(), raw); err != nil {
		t.Fatalf("seed def: %v", err)
	}
}

// seedAgentDefinitionAt seeds a running agent definition with an explicit
// UpdatedAt timestamp, used by heartbeat staleness tests.
func seedAgentDefinitionAt(t *testing.T, kv *fakeMutableKV, id uuid.UUID, updatedAt time.Time) {
	t.Helper()
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      "alpha",
		Type:      "assistant",
		Template:  "default",
		Lifecycle: sextantproto.LifecycleRunning,
		Version:   1,
		CreatedAt: sextantproto.NowTimestamp(),
		UpdatedAt: sextantproto.AtTimestamp(updatedAt),
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	if _, err := kv.Put(context.Background(), id.String(), raw); err != nil {
		t.Fatalf("seed def: %v", err)
	}
}

// TestPromptAgentRefusesOnStaleHeartbeat verifies that when the agent's
// last heartbeat is older than the configured staleness threshold, the
// handler returns ErrCodeAgentNotReachable.
func TestPromptAgentRefusesOnStaleHeartbeat(t *testing.T) {
	now := time.Now()
	id := uuid.New()
	kv := newFakeMutableKV()
	seedAgentDefinitionAt(t, kv, id, now.Add(-120*time.Second))

	hb := &fakeHeartbeats{
		lastSeen: map[uuid.UUID]time.Time{
			id: now.Add(-60 * time.Second), // 60s old
		},
	}
	h := handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions:        kv,
		NATS:               nil,
		From:               sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
		Heartbeats:         hb,
		HeartbeatStaleness: 30 * time.Second,
		Now:                func() time.Time { return now },
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.PromptAgentRequest{AgentID: id, Content: "ping"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error, got ok response")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeAgentNotReachable {
		t.Errorf("Error.Code = %q, want %q", cap.resp.Error.Code, sextantproto.ErrCodeAgentNotReachable)
	}
}

// TestPromptAgentAcceptsFreshHeartbeat verifies that a heartbeat 1s old
// passes the default 30s threshold and the handler succeeds.
func TestPromptAgentAcceptsFreshHeartbeat(t *testing.T) {
	// We need a real NATS connection for the publish path; skip if not available.
	// Instead, verify success by confirming no error and that the emit path
	// would reach the publish step. Since we can't wire NATS in unit tests,
	// we confirm the heartbeat check passes by expecting the error to be due
	// to NATS (nil conn), not the heartbeat guard.
	now := time.Now()
	id := uuid.New()
	kv := newFakeMutableKV()
	seedAgentDefinitionAt(t, kv, id, now.Add(-120*time.Second))

	hb := &fakeHeartbeats{
		lastSeen: map[uuid.UUID]time.Time{
			id: now.Add(-1 * time.Second), // 1s old — fresh
		},
	}
	h := handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions:        kv,
		NATS:               nil, // will panic/error at publish, not at heartbeat check
		From:               sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
		Heartbeats:         hb,
		HeartbeatStaleness: 30 * time.Second,
		Now:                func() time.Time { return now },
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.PromptAgentRequest{AgentID: id, Content: "ping"})
	// The handler should fail at NATS publish (nil conn), not at heartbeat guard.
	_ = h(context.Background(), req, cap.emit())
	// If we got an ErrCodeAgentNotReachable, the heartbeat guard fired incorrectly.
	if cap.resp.Error != nil && cap.resp.Error.Code == sextantproto.ErrCodeAgentNotReachable {
		t.Errorf("heartbeat guard fired for fresh heartbeat: %s", cap.resp.Error.Message)
	}
}

// TestPromptAgentAcceptsRunningAgentInStartupGrace verifies that a running
// agent with no heartbeat but an UpdatedAt within the startup grace period
// is accepted.
func TestPromptAgentAcceptsRunningAgentInStartupGrace(t *testing.T) {
	now := time.Now()
	id := uuid.New()
	kv := newFakeMutableKV()
	// UpdatedAt 15s ago, grace 60s — still within grace.
	seedAgentDefinitionAt(t, kv, id, now.Add(-15*time.Second))

	hb := &fakeHeartbeats{
		lastSeen: map[uuid.UUID]time.Time{}, // no heartbeat yet
	}
	h := handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions:           kv,
		NATS:                  nil,
		From:                  sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
		Heartbeats:            hb,
		HeartbeatStaleness:    30 * time.Second,
		HeartbeatStartupGrace: 60 * time.Second,
		Now:                   func() time.Time { return now },
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.PromptAgentRequest{AgentID: id, Content: "ping"})
	_ = h(context.Background(), req, cap.emit())
	// The heartbeat guard must not fire for an agent within startup grace.
	if cap.resp.Error != nil && cap.resp.Error.Code == sextantproto.ErrCodeAgentNotReachable {
		t.Errorf("heartbeat guard fired within startup grace: %s", cap.resp.Error.Message)
	}
}

// TestPromptAgentRefusesRunningAgentBeyondStartupGrace verifies that a
// running agent with no heartbeat and UpdatedAt > grace ago is refused.
func TestPromptAgentRefusesRunningAgentBeyondStartupGrace(t *testing.T) {
	now := time.Now()
	id := uuid.New()
	kv := newFakeMutableKV()
	// UpdatedAt 5 minutes ago, grace 60s — well beyond grace.
	seedAgentDefinitionAt(t, kv, id, now.Add(-5*time.Minute))

	hb := &fakeHeartbeats{
		lastSeen: map[uuid.UUID]time.Time{}, // no heartbeat
	}
	h := handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions:           kv,
		NATS:                  nil,
		From:                  sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
		Heartbeats:            hb,
		HeartbeatStaleness:    30 * time.Second,
		HeartbeatStartupGrace: 60 * time.Second,
		Now:                   func() time.Time { return now },
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.PromptAgentRequest{AgentID: id, Content: "ping"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error, got ok response")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeAgentNotReachable {
		t.Errorf("Error.Code = %q, want %q", cap.resp.Error.Code, sextantproto.ErrCodeAgentNotReachable)
	}
}

// TestPromptAgentRefusesLostLifecycle verifies that a `lost` lifecycle
// is refused with ErrCodeAgentNotReachable and that the message names
// the lifecycle explicitly.
func TestPromptAgentRefusesLostLifecycle(t *testing.T) {
	kv := newFakeMutableKV()
	id := uuid.New()
	seedAgentDefinition(t, kv, id, "alpha", sextantproto.LifecycleLostState)

	h := handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions: kv,
		NATS:        nil,
		From:        sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"},
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.PromptAgentRequest{AgentID: id, Content: "ping"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error, got ok response")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeAgentNotReachable {
		t.Errorf("Error.Code = %q, want %q", cap.resp.Error.Code, sextantproto.ErrCodeAgentNotReachable)
	}
	if !strings.Contains(cap.resp.Error.Message, "lifecycle=lost") {
		t.Errorf("Error.Message = %q, want substring %q", cap.resp.Error.Message, "lifecycle=lost")
	}
}

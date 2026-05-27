package handlers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

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
			wantRemedy: "sextant agents resume",
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

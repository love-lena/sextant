package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// PromptDeps is the dep bag for the prompt_agent handler.
type PromptDeps struct {
	Definitions AgentMutableKV
	NATS        *nats.Conn
	From        sextantproto.Address
}

// PromptPayload is the body of an envelope published to
// agents.<uuid>.inbox by `prompt_agent`. The sidecar (M11 scaffold)
// logs these to stderr; the real SDK driver loop consumes them
// post-Phase-1.
//
// The "from" field is the operator/daemon address that issued the
// prompt; the envelope's From also carries this — duplicated for
// future-proofing the SDK consumer.
type PromptPayload struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	From    string `json:"from,omitempty"`
}

// promptUnreachableMessage formats the operator-facing error body when
// prompt_agent refuses because the agent's lifecycle isn't running.
// Includes the remedy command for terminal lifecycles so the CLI can
// pass-through to the operator without rewording.
func promptUnreachableMessage(agentID uuid.UUID, lifecycle sextantproto.LifecycleState) string {
	switch lifecycle {
	case sextantproto.LifecycleEndedState, sextantproto.LifecycleCrashedState:
		return fmt.Sprintf("agent %s lifecycle=%s; restart with `sextant agents restart %s`",
			agentID, lifecycle, agentID)
	case sextantproto.LifecyclePaused:
		return fmt.Sprintf("agent %s lifecycle=paused; resume with `sextant agents resume %s`",
			agentID, agentID)
	case sextantproto.LifecycleArchived:
		return fmt.Sprintf("agent %s lifecycle=archived; spawn a new agent instead", agentID)
	case sextantproto.LifecycleDefined:
		return fmt.Sprintf("agent %s lifecycle=defined; start with `sextant agents restart %s`",
			agentID, agentID)
	default:
		return fmt.Sprintf("agent %s lifecycle=%s, want running", agentID, lifecycle)
	}
}

// NewPromptAgent returns a Handler for the prompt_agent verb. Flow:
//
//  1. Decode args.
//  2. Look up the AgentDefinition; error 404 if missing.
//  3. Reject if the agent is not currently `running`.
//  4. Publish an envelope to agents.<uuid>.inbox with kind=prompt.
//  5. Reply {ok: true}.
//
// Reply-or-ack on the sidecar side is out of scope for M11 (the spec
// covers it as part of the Claude Code SDK driver loop, which is
// deferred). The publish itself is the contract.
func NewPromptAgent(deps PromptDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.PromptAgentRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode prompt_agent payload: %v", err))
		}
		if args.AgentID == uuid.Nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "agent_id is required")
		}
		if args.Content == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "content is required")
		}

		entry, err := deps.Definitions.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load definition: %v", err))
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("decode definition: %v", err))
		}
		if def.Lifecycle != sextantproto.LifecycleRunning {
			return emitErr(emit, sextantproto.ErrCodeAgentNotReachable,
				promptUnreachableMessage(args.AgentID, def.Lifecycle))
		}

		payload := PromptPayload{
			Kind:    "prompt",
			Content: args.Content,
			From:    deps.From.ID,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("marshal payload: %v", err))
		}
		env := req.Child(sextantproto.KindAgentFrame, deps.From, raw)
		body, err := json.Marshal(env)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("marshal envelope: %v", err))
		}
		subject := "agents." + args.AgentID.String() + ".inbox"
		if err := deps.NATS.Publish(subject, body); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("publish to %s: %v", subject, err))
		}
		return emitOK(emit, sextantproto.PromptAgentResponse{OK: true})
	}
}

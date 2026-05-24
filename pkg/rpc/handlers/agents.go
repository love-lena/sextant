package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// AgentDefinitionsBucket is the canonical KV bucket for agent
// definitions. See pkg/natsboot/layout.go.
const AgentDefinitionsBucket = "agent_definitions"

// AgentKV is the minimal NATS KV surface the agent handlers need. The
// fake test implementation satisfies it without spinning up a real
// JetStream KV bucket.
type AgentKV interface {
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error)
}

// NewListAgents returns a Handler for the list_agents verb. It walks
// every key in the agent_definitions bucket, decodes the value into
// an AgentDefinition, and projects an AgentSummary into the reply.
//
// In M7 the bucket is always empty (no agent has been spawned yet —
// M11 adds spawning); the handler still returns the empty-list shape
// rather than a "no entries" error so the spec's acceptance criterion
// holds.
func NewListAgents(kv AgentKV) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.ListAgentsRequest
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &args); err != nil {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("decode list_agents payload: %v", err))
			}
		}
		lister, err := kv.ListKeys(ctx)
		if err != nil {
			// Empty bucket can surface either as an empty channel or as
			// "no keys" depending on the jetstream version; we treat
			// both as success-with-empty.
			if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrNoKeysFound) {
				return emitOK(emit, sextantproto.ListAgentsResponse{Agents: []sextantproto.AgentSummary{}})
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("list agent_definitions keys: %v", err))
		}
		defer func() { _ = lister.Stop() }()

		summaries := make([]sextantproto.AgentSummary, 0)
		for key := range lister.Keys() {
			entry, err := kv.Get(ctx, key)
			if err != nil {
				if errors.Is(err, jetstream.ErrKeyNotFound) {
					// Key disappeared between List and Get; skip.
					continue
				}
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("read agent_definitions/%s: %v", key, err))
			}
			var def sextantproto.AgentDefinition
			if err := json.Unmarshal(entry.Value(), &def); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("decode agent_definitions/%s: %v", key, err))
			}
			if args.Filter != nil && args.Filter.Lifecycle != "" && string(def.Lifecycle) != args.Filter.Lifecycle {
				continue
			}
			summaries = append(summaries, sextantproto.AgentSummary{
				UUID:      def.UUID,
				Name:      def.Name,
				Type:      def.Type,
				Template:  def.Template,
				Lifecycle: string(def.Lifecycle),
				Version:   def.Version,
				UpdatedAt: def.UpdatedAt.Time,
			})
		}
		return emitOK(emit, sextantproto.ListAgentsResponse{Agents: summaries})
	}
}

// NewGetAgentStatus returns a Handler for the get_agent_status verb.
// On a missing agent the handler emits an RPCError with code
// "agent_not_found" — M7 acceptance asserts this is the 404 path.
func NewGetAgentStatus(kv AgentKV) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.GetAgentStatusRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode get_agent_status payload: %v", err))
		}
		if args.AgentID == uuid.Nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "agent_id is required")
		}
		entry, err := kv.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("read agent_definitions/%s: %v", args.AgentID, err))
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("decode agent_definitions/%s: %v", args.AgentID, err))
		}
		return emitOK(emit, sextantproto.GetAgentStatusResponse{
			Status: sextantproto.AgentStatus{
				UUID:      def.UUID,
				Name:      def.Name,
				Lifecycle: string(def.Lifecycle),
				Version:   def.Version,
				UpdatedAt: def.UpdatedAt.Time,
			},
		})
	}
}

// NewReadFile returns the M7 stub handler for the read_file verb.
// Real implementation lands in M11 when container management is in
// scope.
func NewReadFile() rpc.Handler {
	return func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		return emitErr(emit, "not_implemented",
			"read_file ships in M11+ when container management lands")
	}
}

// emitOK marshals result and emits a terminal success response.
func emitOK(emit func(sextantproto.RPCResponse), result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return emitErr(emit, sextantproto.ErrCodeInternal,
			fmt.Sprintf("marshal result: %v", err))
	}
	emit(sextantproto.RPCResponse{Result: raw, Terminal: true})
	return nil
}

// emitErr emits a terminal error response.
func emitErr(emit func(sextantproto.RPCResponse), code, msg string) error {
	emit(sextantproto.RPCResponse{
		Error:    &sextantproto.RPCError{Code: code, Message: msg},
		Terminal: true,
	})
	return nil
}

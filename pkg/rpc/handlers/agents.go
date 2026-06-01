package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
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

// GetAgentStatusDeps is the dep bag for the get_agent_status handler.
// KV is required (the canonical agent record source); Heartbeats and Now
// are optional and only used when a request opts into IncludeHeartbeat.
//
// Heartbeats can be nil — the handler still serves the L0 KV record,
// just without the freshness annotation. That matches the daemon's
// fail-soft posture when the cache failed to start.
//
// AgentsDataRoot, when non-empty, populates Status.SessionLog on every
// response — the handler computes the per-agent claude-projects host
// path via handlers.AgentProjectsDir(root, uuid). When empty (older
// daemons + most unit tests), SessionLog stays nil.
type GetAgentStatusDeps struct {
	KV             AgentKV
	Heartbeats     HeartbeatLookup
	AgentsDataRoot string
	Now            func() time.Time
}

// NewGetAgentStatus returns a Handler for the get_agent_status verb.
// On a missing agent the handler emits an RPCError with code
// "agent_not_found" — M7 acceptance asserts this is the 404 path.
//
// When the request sets IncludeHeartbeat=true and a HeartbeatLookup is
// wired, the response's AgentStatus.Heartbeat is populated with the
// last observed timestamp + age. Defense-in-depth for
// `sextant agents check` per
// `slug:feat-agents-check-heartbeat-secondary-signal`.
func NewGetAgentStatus(kv AgentKV) rpc.Handler {
	return NewGetAgentStatusWithDeps(GetAgentStatusDeps{KV: kv})
}

// NewGetAgentStatusWithDeps is the dep-bag form. Existing callers that
// only care about the KV record keep the narrow NewGetAgentStatus
// signature; sextantd opts in to the dep bag to wire the heartbeat
// cache.
func NewGetAgentStatusWithDeps(deps GetAgentStatusDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.GetAgentStatusRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode get_agent_status payload: %v", err))
		}
		if args.AgentID == uuid.Nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "agent_id is required")
		}
		entry, err := deps.KV.Get(ctx, args.AgentID.String())
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
		status := sextantproto.AgentStatus{
			UUID:      def.UUID,
			Name:      def.Name,
			Lifecycle: string(def.Lifecycle),
			Version:   def.Version,
			UpdatedAt: def.UpdatedAt.Time,
		}
		if args.IncludeHeartbeat {
			status.Heartbeat = buildHeartbeatSnapshot(deps, def.UUID)
		}
		if deps.AgentsDataRoot != "" {
			info := &sextantproto.SessionLogInfo{
				ProjectsDir: AgentProjectsDir(deps.AgentsDataRoot, def.UUID),
			}
			if def.Runtime.SessionID != nil {
				info.SessionID = *def.Runtime.SessionID
			}
			status.SessionLog = info
		}
		return emitOK(emit, sextantproto.GetAgentStatusResponse{Status: status})
	}
}

// buildHeartbeatSnapshot projects the cache lookup into the wire shape.
// Always returns a non-nil snapshot when the caller asked, so the
// "source" field is always set — that lets clients distinguish "the
// cache had nothing for this agent" from "the daemon doesn't have a
// cache wired" by inspecting the source.
func buildHeartbeatSnapshot(deps GetAgentStatusDeps, id uuid.UUID) *sextantproto.HeartbeatSnapshot {
	if deps.Heartbeats == nil {
		return &sextantproto.HeartbeatSnapshot{Source: "none"}
	}
	last, ok := deps.Heartbeats.LastSeen(id)
	if !ok {
		return &sextantproto.HeartbeatSnapshot{Source: "none"}
	}
	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	age := nowFn().Sub(last).Seconds()
	return &sextantproto.HeartbeatSnapshot{
		LastSeen:   &last,
		AgeSeconds: &age,
		Source:     "cache",
	}
}

// NewReadFileStub returns the M7-era stub handler for the read_file
// verb. M12 ships a real implementation (NewReadFile) in files.go —
// the stub is kept for tests that need a fast NotImplemented response
// without spinning up a container backend.
func NewReadFileStub() rpc.Handler {
	return func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		return emitErr(emit, sextantproto.ErrCodeNotImplemented,
			"read_file stub: deps not configured")
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

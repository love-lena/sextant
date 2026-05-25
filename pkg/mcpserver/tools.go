package mcpserver

// Tool capability constants. These are the capability strings the JWT
// must carry for an agent caller to invoke each MCP tool. Operator
// callers (stdio transport) bypass the check.
//
// The hierarchy mirrors specs/protocols/rpc-catalog.md "MCP tool
// capabilities": communication caps are their own names; introspection
// piggybacks on the existing read.* caps; control reuses the RPC verbs'
// control.* caps; system gets two new caps (emit_event, read.metrics).
const (
	CapSendMessage     = "send_message"
	CapBroadcast       = "broadcast"
	CapReadAgents      = "read.agents"
	CapReadHistory     = "read.history"
	CapReadWorktrees   = "read.worktrees"
	CapControlSpawn    = "control.spawn"
	CapControlKill     = "control.kill"
	CapControlPrompt   = "control.prompt"
	CapControlWorktree = "control.worktree"
	CapEmitEvent       = "emit_event"
	CapReadMetrics     = "read.metrics"
)

// Tool names. Re-used by the audit envelope (`audit.tool_call`) and by
// the sidecar smoke tests so a typo doesn't get baked into the wire
// format.
const (
	ToolSendMessage     = "send_message"
	ToolBroadcast       = "broadcast"
	ToolListAgents      = "list_agents"
	ToolAgentStatus     = "agent_status"
	ToolQueryAudit      = "query_audit"
	ToolSpawnAgent      = "spawn_agent"
	ToolKillAgent       = "kill_agent"
	ToolPromptAgent     = "prompt_agent"
	ToolEmitEvent       = "emit_event"
	ToolGetMetric       = "get_metric"
	ToolWorktreeCreate  = "worktree_create"
	ToolWorktreeDestroy = "worktree_destroy"
	ToolWorktreeList    = "worktree_list"
	ToolWorktreeMerge   = "worktree_merge"
	ToolWorktreeDiff    = "worktree_diff"
)

// AllTools returns the M10+M14 tool catalog in catalog order. Stable
// order matters for tools/list responses — tests pin against the
// slice.
func AllTools() []string {
	return []string{
		ToolSendMessage,
		ToolBroadcast,
		ToolListAgents,
		ToolAgentStatus,
		ToolQueryAudit,
		ToolSpawnAgent,
		ToolKillAgent,
		ToolPromptAgent,
		ToolEmitEvent,
		ToolGetMetric,
		ToolWorktreeCreate,
		ToolWorktreeDestroy,
		ToolWorktreeList,
		ToolWorktreeMerge,
		ToolWorktreeDiff,
	}
}

// CapForTool returns the capability string required to invoke tool. An
// unknown tool returns the empty string; the dispatcher treats that as
// "deny by default" for agent callers (HasCap("") is true, but
// register paths assert a non-empty cap when adding the tool).
func CapForTool(tool string) string {
	switch tool {
	case ToolSendMessage:
		return CapSendMessage
	case ToolBroadcast:
		return CapBroadcast
	case ToolListAgents, ToolAgentStatus:
		return CapReadAgents
	case ToolQueryAudit:
		return CapReadHistory
	case ToolSpawnAgent:
		return CapControlSpawn
	case ToolKillAgent:
		return CapControlKill
	case ToolPromptAgent:
		return CapControlPrompt
	case ToolEmitEvent:
		return CapEmitEvent
	case ToolGetMetric:
		return CapReadMetrics
	case ToolWorktreeCreate, ToolWorktreeDestroy, ToolWorktreeMerge:
		return CapControlWorktree
	case ToolWorktreeList, ToolWorktreeDiff:
		return CapReadWorktrees
	default:
		return ""
	}
}

// --- Tool argument shapes ---------------------------------------------------
//
// These are the typed argument structs the MCP SDK uses to derive each
// tool's JSON Schema. Keeping them in this package keeps the catalog
// self-describing: the tool spec, the cap, and the schema all live next
// to each other.
//
// jsonschema tags are bare descriptions per
// github.com/google/jsonschema-go.

// SendMessageArgs is the argument shape for send_message.
type SendMessageArgs struct {
	ToAgent string `json:"to_agent" jsonschema:"UUID of the agent whose inbox to publish to"`
	Content string `json:"content" jsonschema:"Free-form message body delivered to the agent"`
}

// BroadcastArgs is the argument shape for broadcast.
type BroadcastArgs struct {
	Subject string `json:"subject" jsonschema:"Subject under broadcast.* to publish on (e.g. team.dev)"`
	Content string `json:"content" jsonschema:"Free-form message body"`
}

// ListAgentsArgs is the argument shape for list_agents. Filter mirrors
// sextantproto.ListAgentsFilter (omitted fields = no filter).
type ListAgentsArgs struct {
	Lifecycle string `json:"lifecycle,omitempty" jsonschema:"Optional lifecycle filter: defined|running|paused|archived"`
}

// AgentStatusArgs is the argument shape for agent_status.
type AgentStatusArgs struct {
	AgentID string `json:"agent_id" jsonschema:"Agent UUID to fetch status for"`
}

// QueryAuditArgs is the argument shape for query_audit. Mirrors
// sextantproto.QueryHistoryRequest scoped to audit subjects.
type QueryAuditArgs struct {
	Actor  string `json:"actor,omitempty" jsonschema:"Optional actor filter (UUID for agents, 'operator' for operator)"`
	Action string `json:"action,omitempty" jsonschema:"Optional action filter (e.g. tool_call.send_message)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max rows to return (default 1000, capped at 10000)"`
}

// SpawnAgentArgs is the argument shape for spawn_agent (M11 stub).
type SpawnAgentArgs struct {
	Name     string `json:"name" jsonschema:"Human-readable agent name"`
	Template string `json:"template" jsonschema:"Template name (see ~/.config/sextant/templates/)"`
}

// KillAgentArgs is the argument shape for kill_agent (M11 stub).
type KillAgentArgs struct {
	AgentID      string `json:"agent_id" jsonschema:"Agent UUID to kill"`
	GraceSeconds int    `json:"grace_seconds,omitempty" jsonschema:"Seconds to wait before SIGKILL (default 10)"`
}

// PromptAgentArgs is the argument shape for prompt_agent (M11 stub).
type PromptAgentArgs struct {
	AgentID string `json:"agent_id" jsonschema:"Agent UUID to prompt"`
	Content string `json:"content" jsonschema:"Prompt body"`
}

// EmitEventArgs is the argument shape for emit_event.
type EmitEventArgs struct {
	Subject string         `json:"subject" jsonschema:"NATS subject under sextant.system.* to publish on"`
	Payload map[string]any `json:"payload,omitempty" jsonschema:"Free-form event payload"`
}

// GetMetricArgs is the argument shape for get_metric.
type GetMetricArgs struct {
	Name      string `json:"name" jsonschema:"Metric name (e.g. agents.active)"`
	SinceSecs int    `json:"since_seconds,omitempty" jsonschema:"Lookback window in seconds (default 300)"`
}

// --- Tool result shapes -----------------------------------------------------

// SendMessageResult is the success shape returned by send_message.
type SendMessageResult struct {
	OK      bool   `json:"ok"`
	Subject string `json:"subject"`
}

// BroadcastResult is the success shape returned by broadcast.
type BroadcastResult struct {
	OK      bool   `json:"ok"`
	Subject string `json:"subject"`
}

// EmitEventResult is the success shape returned by emit_event.
type EmitEventResult struct {
	OK      bool   `json:"ok"`
	Subject string `json:"subject"`
}

// --- M14 worktree tool shapes -----------------------------------------------

// WorktreeCreateArgs is the argument shape for worktree_create.
type WorktreeCreateArgs struct {
	Name       string `json:"name" jsonschema:"Worktree name (also the branch name); must match <kind>-<desc>-<seq>"`
	BaseBranch string `json:"base_branch,omitempty" jsonschema:"Branch to fork from (default main)"`
}

// WorktreeDestroyArgs is the argument shape for worktree_destroy.
type WorktreeDestroyArgs struct {
	Name  string `json:"name" jsonschema:"Worktree name to destroy"`
	Force bool   `json:"force,omitempty" jsonschema:"Operator override: destroy even when status != archived/merged"`
}

// WorktreeListArgs is the argument shape for worktree_list. Empty
// today; reserved for future filtering.
type WorktreeListArgs struct{}

// WorktreeMergeArgs is the argument shape for worktree_merge.
type WorktreeMergeArgs struct {
	Name   string `json:"name" jsonschema:"Worktree name (branch is the same)"`
	Target string `json:"target,omitempty" jsonschema:"Target branch (default main)"`
}

// WorktreeDiffArgs is the argument shape for worktree_diff.
type WorktreeDiffArgs struct {
	Name    string `json:"name" jsonschema:"Worktree name"`
	Against string `json:"against,omitempty" jsonschema:"Branch to diff against (default main)"`
}

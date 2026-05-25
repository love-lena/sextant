package rpc

// CapFor returns the capability string required to invoke the given verb
// per specs/protocols/rpc-catalog.md "Catalog by category". Returns "" for
// unknown verbs — callers should treat that as "deny by default" once
// M10 wires real JWT checks; in M7 the operator-path CheckCap stub
// returns nil regardless.
func CapFor(verb string) string {
	switch verb {
	case VerbListAgents, VerbGetAgentStatus:
		return "read.agents"
	case VerbQueryHistory, VerbQueryAudit, VerbQueryTrace:
		return "read.history"
	case VerbReadFile, VerbListDir, VerbStat:
		return "read.container_files"
	case VerbExecInContainer:
		return "control.exec"
	case VerbSpawnAgent:
		return "control.spawn"
	case VerbKillAgent:
		return "control.kill"
	case VerbRestartAgent:
		return "control.restart"
	case VerbPromptAgent:
		return "control.prompt"
	case VerbWorktreeCreate, VerbWorktreeDestroy, VerbWorktreeMerge:
		return "control.worktree"
	case VerbWorktreeList, VerbWorktreeDiff:
		return "read.worktrees"
	default:
		return ""
	}
}

// Verb names. One per row in specs/protocols/rpc-catalog.md.
const (
	VerbListAgents     = "list_agents"
	VerbGetAgentStatus = "get_agent_status"
	VerbReadFile       = "read_file"
	VerbQueryHistory   = "query_history"
	// M11 agent-lifecycle verbs. Real implementations land in
	// pkg/rpc/handlers/{spawn,kill,prompt}.go.
	VerbSpawnAgent  = "spawn_agent"
	VerbKillAgent   = "kill_agent"
	VerbPromptAgent = "prompt_agent"
	// M12 verbs. Real implementations land in
	// pkg/rpc/handlers/{restart,files,exec,query_audit,query_trace}.go.
	VerbRestartAgent    = "restart_agent"
	VerbListDir         = "list_dir"
	VerbStat            = "stat"
	VerbExecInContainer = "exec_in_container"
	VerbQueryAudit      = "query_audit"
	VerbQueryTrace      = "query_trace"
	// M14 worktree verbs. Real implementations land in
	// pkg/rpc/handlers/worktree.go.
	VerbWorktreeCreate  = "worktree_create"
	VerbWorktreeDestroy = "worktree_destroy"
	VerbWorktreeList    = "worktree_list"
	VerbWorktreeMerge   = "worktree_merge"
	VerbWorktreeDiff    = "worktree_diff"
)

// QueryHistoryDefaultLimit is the row cap when the request omits Limit.
const QueryHistoryDefaultLimit = 1000

// QueryHistoryMaxLimit is the absolute ceiling clients can request. Higher
// values are silently clamped to this. The cap exists to keep one bad
// request from pulling an arbitrary slice of history into the daemon's
// memory.
const QueryHistoryMaxLimit = 10000

// Wire payload structs (ListAgentsRequest, GetAgentStatusResponse,
// QueryHistoryFilter, etc.) live in pkg/sextantproto so their JSON
// Schemas can be regenerated for the TypeScript client. Import from
// sextantproto directly.

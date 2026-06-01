package rpc

// CapFor and the VerbSpecs table that backs it live in verbspec.go —
// the single declarative RPC surface (RFC §5.8). This file holds the
// verb-name constants the table references and the query_history limit
// knobs.

// Verb names. One per row in specs/protocols/rpc-catalog.md.
const (
	VerbListAgents     = "list_agents"
	VerbGetAgentStatus = "get_agent_status"
	VerbReadFile       = "read_file"
	VerbQueryHistory   = "query_history"
	// M11 agent-lifecycle verbs. Real implementations land in
	// pkg/rpc/handlers/{spawn,kill,prompt}.go.
	VerbSpawnAgent   = "spawn_agent"
	VerbKillAgent    = "kill_agent"
	VerbPromptAgent  = "prompt_agent"
	VerbArchiveAgent = "archive_agent"
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
	// Diagnostic verb. Implementation in pkg/rpc/handlers/version.go.
	// Surfaces daemon build + proto + pid + start time so `sextant
	// doctor` can flag CLI/daemon drift after `make install` without a
	// daemon restart. See slug:feat-doctor-show-daemon-version.
	VerbGetVersion = "get_version"
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

package rpc

import "github.com/love-lena/sextant/pkg/sextantproto"

// Phase tags when a verb's handler is wired during daemon startup. The
// RPC surface comes up in stages (cmd/sextantd): the query-only verbs
// that have no container-runtime dependency register first in startRPC
// (PhaseInitial); the agent-lifecycle / container verbs register once
// the spawn runtime is built (PhaseLifecycle); and the worktree verbs
// register last, conditionally, once the worktree runtime is built
// (PhaseWorktree). Encoding the stage on the spec lets each daemon
// registration step iterate one table and still preserve the staged
// registration order.
type Phase int

const (
	// PhaseInitial verbs register in startRPC — no container-runtime
	// dependency (ClickHouse-only queries, KV reads, diagnostics).
	PhaseInitial Phase = iota
	// PhaseLifecycle verbs register after the spawn runtime is built —
	// they drive Docker (spawn/kill/restart/exec) or read container
	// filesystems.
	PhaseLifecycle
	// PhaseWorktree verbs register after the worktree runtime is built,
	// and only when worktree support is enabled (the registration is a
	// no-op when the runtime is nil).
	PhaseWorktree
)

// VerbSpec is one row of the single declarative RPC surface table. It
// folds together the four enumerations that used to drift independently
// (RFC §5.8):
//
//   - the Verb* name constant (Name),
//   - the capability CapFor used to switch on (Capability),
//   - the handler-registration call in cmd/sextantd/rpc.go (Phase tells
//     the daemon which stage registers it; the daemon supplies the
//     handler factory keyed by Name and iterates this table), and
//   - the schema generator's hand-maintained payload list (Req/Resp are
//     the zero-value sample structs sextantproto-gen reflects).
//
// Adding a verb is now one row: forget nothing because there is one
// place to add it.
type VerbSpec struct {
	// Name is the wire verb (the trailing token of sextant.rpc.<verb>).
	// Must be one of the Verb* constants.
	Name string
	// Capability is the capability string an envelope must carry to
	// invoke the verb; "" means "no capability required" (the
	// AllowAll-era diagnostic lane). Read by CapFor.
	Capability string
	// Phase is the registration stage (PhaseInitial vs PhaseLifecycle).
	Phase Phase
	// Req / Resp are zero-value sample payload structs the schema
	// generator reflects into JSON Schema. Nil for verbs whose payloads
	// are intentionally not part of the generated client surface.
	Req  any
	Resp any
}

// VerbSpecs is the single source of truth for the RPC surface. Dispatch
// registration iterates it (cmd/sextantd/rpc.go), CapFor reads it, and
// the schema generator (cmd/sextantproto-gen) walks its Req/Resp types.
//
// Order within a phase is the registration order the daemon emits and
// must be preserved (the two-stage ordering is part of the contract —
// see the registration code and its tests). The schema generator walks
// the same slice for the verb-payload portion of its output, so this
// order also pins the generator's emit order for those entries; do not
// reorder without confirming `go generate ./...` stays byte-identical.
var VerbSpecs = []VerbSpec{
	// --- PhaseInitial: query / diagnostic verbs (no container deps) ---
	{
		Name:       VerbListAgents,
		Capability: "read.agents",
		Phase:      PhaseInitial,
		Req:        &sextantproto.ListAgentsRequest{},
		Resp:       &sextantproto.ListAgentsResponse{},
	},
	{
		Name:       VerbGetAgentStatus,
		Capability: "read.agents",
		Phase:      PhaseInitial,
		Req:        &sextantproto.GetAgentStatusRequest{},
		Resp:       &sextantproto.GetAgentStatusResponse{},
	},
	{
		Name:       VerbQueryHistory,
		Capability: "read.history",
		Phase:      PhaseInitial,
		Req:        &sextantproto.QueryHistoryRequest{},
		Resp:       &sextantproto.QueryHistoryResponse{},
	},
	{
		Name:       VerbQueryAudit,
		Capability: "read.history",
		Phase:      PhaseInitial,
		Req:        &sextantproto.QueryAuditRequest{},
		Resp:       &sextantproto.QueryAuditResponse{},
	},
	{
		Name:       VerbQueryTrace,
		Capability: "read.history",
		Phase:      PhaseInitial,
		Req:        &sextantproto.QueryTraceRequest{},
		Resp:       &sextantproto.QueryTraceResponse{},
	},
	{
		// get_version is a diagnostic verb — `sextant doctor` calls it
		// to compare CLI and daemon builds. The reply carries no agent
		// state, only build metadata; an operator with operator creds
		// can already read more sensitive surfaces. The empty capability
		// keeps the verb in the same lane as "no capability required"
		// for M7's AllowAll checker, and gives M10 an obvious slot to
		// wire a per-verb policy without changing this signature.
		Name:       VerbGetVersion,
		Capability: "",
		Phase:      PhaseInitial,
		Req:        &sextantproto.GetVersionRequest{},
		Resp:       &sextantproto.GetVersionResponse{},
	},

	// --- PhaseLifecycle: agent-lifecycle + container verbs ---
	{
		Name:       VerbSpawnAgent,
		Capability: "control.spawn",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.SpawnAgentRequest{},
		Resp:       &sextantproto.SpawnAgentResponse{},
	},
	{
		Name:       VerbKillAgent,
		Capability: "control.kill",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.KillAgentRequest{},
		Resp:       &sextantproto.KillAgentResponse{},
	},
	{
		Name:       VerbArchiveAgent,
		Capability: "control.archive",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.ArchiveAgentRequest{},
		Resp:       &sextantproto.ArchiveAgentResponse{},
	},
	{
		Name:       VerbPromptAgent,
		Capability: "control.prompt",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.PromptAgentRequest{},
		Resp:       &sextantproto.PromptAgentResponse{},
	},
	{
		Name:       VerbRestartAgent,
		Capability: "control.restart",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.RestartAgentRequest{},
		Resp:       &sextantproto.RestartAgentResponse{},
	},
	{
		Name:       VerbReadFile,
		Capability: "read.container_files",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.ReadFileRequest{},
		Resp:       &sextantproto.ReadFileResponse{},
	},
	{
		Name:       VerbListDir,
		Capability: "read.container_files",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.ListDirRequest{},
		Resp:       &sextantproto.ListDirResponse{},
	},
	{
		Name:       VerbStat,
		Capability: "read.container_files",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.StatRequest{},
		Resp:       &sextantproto.StatResponse{},
	},
	{
		Name:       VerbExecInContainer,
		Capability: "control.exec",
		Phase:      PhaseLifecycle,
		Req:        &sextantproto.ExecInContainerRequest{},
		Resp:       &sextantproto.ExecInContainerResponse{},
	},
	// --- PhaseWorktree: worktree verbs (registered last, conditionally) ---
	{
		Name:       VerbWorktreeCreate,
		Capability: "control.worktree",
		Phase:      PhaseWorktree,
		Req:        &sextantproto.WorktreeCreateRequest{},
		Resp:       &sextantproto.WorktreeCreateResponse{},
	},
	{
		Name:       VerbWorktreeDestroy,
		Capability: "control.worktree",
		Phase:      PhaseWorktree,
		Req:        &sextantproto.WorktreeDestroyRequest{},
		Resp:       &sextantproto.WorktreeDestroyResponse{},
	},
	{
		Name:       VerbWorktreeList,
		Capability: "read.worktrees",
		Phase:      PhaseWorktree,
		Req:        &sextantproto.WorktreeListRequest{},
		Resp:       &sextantproto.WorktreeListResponse{},
	},
	{
		Name:       VerbWorktreeMerge,
		Capability: "control.worktree",
		Phase:      PhaseWorktree,
		Req:        &sextantproto.WorktreeMergeRequest{},
		Resp:       &sextantproto.WorktreeMergeResponse{},
	},
	{
		Name:       VerbWorktreeDiff,
		Capability: "read.worktrees",
		Phase:      PhaseWorktree,
		Req:        &sextantproto.WorktreeDiffRequest{},
		Resp:       &sextantproto.WorktreeDiffResponse{},
	},
}

// VerbSpecsForPhase returns the specs registered in the given phase, in
// table order. The daemon's per-stage registration iterates the result
// so the staged registration order is read off the one table rather than
// hand-maintained in three functions.
func VerbSpecsForPhase(p Phase) []VerbSpec {
	var out []VerbSpec
	for _, s := range VerbSpecs {
		if s.Phase == p {
			out = append(out, s)
		}
	}
	return out
}

// capByVerb is the verb→capability index built once from VerbSpecs.
// CapFor reads it; an unknown verb resolves to "" (deny-by-default once
// M10 wires real JWT checks; the M7 AllowAll stub returns nil
// regardless). Built at package init so CapFor stays allocation-free on
// the hot dispatch path.
var capByVerb = func() map[string]string {
	m := make(map[string]string, len(VerbSpecs))
	for _, s := range VerbSpecs {
		m[s.Name] = s.Capability
	}
	return m
}()

// CapFor returns the capability string required to invoke the given verb
// per specs/protocols/rpc-catalog.md "Catalog by category". Returns ""
// for unknown verbs — callers should treat that as "deny by default"
// once M10 wires real JWT checks; in M7 the operator-path CheckCap stub
// returns nil regardless.
//
// The mapping is read from the single VerbSpecs table (RFC §5.8) so it
// cannot drift from registration or schema generation.
func CapFor(verb string) string {
	return capByVerb[verb]
}

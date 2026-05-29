// Package agentdetail is the Tier 1 component for `sextant agents show
// <id> -i`: a DetailPane inspector for one agent. It assembles from
// existing RPCs — get_agent_status (lifecycle / version / session) +
// list_agents (template/name) + worktree_list (owning worktree) — and
// degrades gracefully when a field is unavailable (no get_agent_detail
// RPC required; see RFC §6 †). Recent-frames / usage rows are a tracked
// follow-up. Per the RFC (P2).
package agentdetail

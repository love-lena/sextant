// agents_check.go owns `sextant agents check <ref>` — a one-shot
// health probe that returns a verdict + remedy command per
// `plans/issues/feat-sextant-agents-check.md`. Pairs with `doctor
// --agents` (bulk variant); both share the AgentCheck struct + the
// runAgentCheck function so verdict logic lives in one place.
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/cliout"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// AgentCheck is the synthesized verdict for one agent. Stable shape
// for --json consumers.
//
// HeartbeatAge / HeartbeatSource carry the L1 freshness signal pulled
// from the daemon's HeartbeatCache via
// get_agent_status?include_heartbeat=true. Populated only when the
// daemon responded with a heartbeat snapshot; nil HeartbeatAge means
// the cache had no entry for this agent (e.g., the agent never
// published a heartbeat, or the daemon's cache failed to wire).
type AgentCheck struct {
	Ref             string         `json:"ref"`
	UUID            uuid.UUID      `json:"uuid,omitempty"`
	Name            string         `json:"name,omitempty"`
	Lifecycle       string         `json:"lifecycle,omitempty"`
	Version         uint64         `json:"version,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at,omitempty"`
	HeartbeatAge    *time.Duration `json:"heartbeat_age_ns,omitempty"`
	HeartbeatSource string         `json:"heartbeat_source,omitempty"`
	Verdict         string         `json:"verdict"`
	Remedy          string         `json:"remedy,omitempty"`
}

// agentChecker abstracts the daemon dependencies runAgentCheck needs.
// Wrapper so tests don't need a real *client.Client.
type agentChecker interface {
	ResolveAgentRef(ctx context.Context, ref string) (uuid.UUID, error)
	GetAgentStatus(ctx context.Context, id uuid.UUID) (sextantproto.AgentStatus, error)
}

// newAgentsCheckCmd builds `sextant agents check <ref>`.
func newAgentsCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <agent>",
		Short: "One-shot health probe (verdict + remedy)",
		Long: `Probe one agent's record + last lifecycle and synthesize a
verdict — healthy, ended, paused, archived, stale_record, not_found —
together with the remedy command for the operator to copy-paste.

Verbose alternative to chaining ` + "`agents show` + `events tail`" + ` +
docker inspection. Pairs with ` + "`sextant doctor --agents`" + ` for the
bulk version that scans every registered agent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			check := runAgentCheck(ctx, &clientChecker{cli: cli}, args[0])
			return renderAgentCheck(cmd, cmd.OutOrStdout(), check, globalFlags.asJSON)
		},
	}
}

// runAgentCheck performs the probes against the provided agentChecker.
// Side-effect-free — the checker abstraction makes the verdict logic
// testable without a live daemon. The synthesized verdicts (in
// priority order):
//
//	not_found     — resolveAgentRef returned not-found
//	rpc_error     — get_agent_status failed for another reason
//	ended         — lifecycle is one of {ended, crashed}
//	archived      — lifecycle is archived
//	paused        — lifecycle is paused
//	degraded      — lifecycle=running but the daemon's HeartbeatCache
//	                shows the heartbeat is older than the staleness
//	                threshold (L2/L3 lifecycle convergence hasn't caught
//	                up yet — defense-in-depth signal)
//	healthy       — lifecycle is running and heartbeat is fresh (or
//	                no heartbeat signal was returned)
//	stale_record  — anything else (defined etc.) for a record we expected to be live
func runAgentCheck(ctx context.Context, ch agentChecker, ref string) AgentCheck {
	out := AgentCheck{Ref: ref}
	id, err := ch.ResolveAgentRef(ctx, ref)
	if err != nil {
		out.Verdict = "not_found"
		out.Remedy = "sextant agents list # confirm name spelling"
		return out
	}
	status, err := ch.GetAgentStatus(ctx, id)
	if err != nil {
		out.UUID = id
		out.Verdict = "rpc_error"
		out.Remedy = "sextant doctor # check daemon reachability"
		return out
	}
	out.UUID = status.UUID
	out.Name = status.Name
	out.Lifecycle = status.Lifecycle
	out.Version = status.Version
	out.UpdatedAt = status.UpdatedAt
	if status.Heartbeat != nil {
		out.HeartbeatSource = status.Heartbeat.Source
		if status.Heartbeat.AgeSeconds != nil {
			age := time.Duration(*status.Heartbeat.AgeSeconds * float64(time.Second))
			out.HeartbeatAge = &age
		}
	}
	switch status.Lifecycle {
	case string(sextantproto.LifecycleRunning):
		// Defense-in-depth: a fresh KV record can still hide a dead
		// daemon path. If the HeartbeatCache says we haven't heard from
		// the sidecar in > staleness, downgrade to `degraded` so the
		// operator looks at the daemon logs instead of trusting
		// lifecycle=running. See
		// `plans/issues/feat-agents-check-heartbeat-secondary-signal.md`.
		if out.HeartbeatAge != nil && *out.HeartbeatAge > sextantd.DefaultHeartbeatStaleness {
			out.Verdict = "degraded"
			out.Remedy = "sextant daemon logs --tail 50"
		} else {
			out.Verdict = "healthy"
		}
	case string(sextantproto.LifecycleEndedState),
		string(sextantproto.LifecycleCrashedState):
		out.Verdict = "ended"
		out.Remedy = fmt.Sprintf("sextant agents restart %s", id)
	case string(sextantproto.LifecycleLostState):
		// `lost` is daemon-inferred death (kill -9, OOM, host crash, or
		// docker daemon restart under a running sidecar). Restart with
		// --preserve-session keeps the agent's session token; the
		// container itself is gone regardless. See
		// [[feat-agents-resume-verb]] for true session-preserving
		// resume.
		out.Verdict = "lost"
		out.Remedy = fmt.Sprintf("sextant agents restart %s --preserve-session", id)
	case string(sextantproto.LifecyclePaused):
		out.Verdict = "paused"
		// No `sextant agents resume` verb today — restart is the only
		// real recovery. See [[feat-agents-resume-verb]] follow-up.
		out.Remedy = fmt.Sprintf("sextant agents restart %s", id)
	case string(sextantproto.LifecycleArchived):
		out.Verdict = "archived"
		out.Remedy = "spawn a new agent instead"
	case string(sextantproto.LifecycleDefined):
		out.Verdict = "stale_record"
		out.Remedy = fmt.Sprintf("sextant agents restart %s", id)
	default:
		out.Verdict = "stale_record"
		out.Remedy = fmt.Sprintf("sextant agents restart %s", id)
	}
	return out
}

// renderAgentCheck writes the check verdict to w. JSON mode wraps the
// AgentCheck in the cliout envelope (`{data: AgentCheck, meta:...}`);
// text mode renders a small table.
func renderAgentCheck(cmd *cobra.Command, w io.Writer, check AgentCheck, asJSON bool) error {
	if asJSON {
		return cliout.WriteEnvelope(w, cliout.EnvelopeFromCommand(cmd, check))
	}
	if check.UUID == uuid.Nil {
		printf(w, "agent: %s\n", check.Ref)
		printf(w, "verdict: %s\n", check.Verdict)
		if check.Remedy != "" {
			printf(w, "remedy: %s\n", check.Remedy)
		}
		return nil
	}
	printf(w, "agent:   %s (%s)\n", check.Name, check.UUID)
	printf(w, "record:  lifecycle=%s version=%d updated=%s\n",
		check.Lifecycle, check.Version, check.UpdatedAt.Format(time.RFC3339))
	if check.HeartbeatSource != "" {
		// Show the L1 heartbeat freshness reading. The operator uses
		// this line to decide whether to trust the `verdict` for a
		// running agent. "age=none" means the daemon's HeartbeatCache
		// has no sample yet.
		if check.HeartbeatAge != nil {
			printf(w, "heartbeat: age=%s source=%s\n",
				check.HeartbeatAge.Truncate(time.Second), check.HeartbeatSource)
		} else {
			printf(w, "heartbeat: age=none source=%s\n", check.HeartbeatSource)
		}
	}
	printf(w, "verdict: %s\n", check.Verdict)
	if check.Remedy != "" {
		printf(w, "remedy:  %s\n", check.Remedy)
	}
	return nil
}

// clientChecker adapts a real *client.Client to the agentChecker
// interface. Used by the cobra wiring; tests inject their own fake.
type clientChecker struct {
	cli *client.Client
}

func (c *clientChecker) ResolveAgentRef(ctx context.Context, ref string) (uuid.UUID, error) {
	return resolveAgentRef(ctx, c.cli, ref)
}

func (c *clientChecker) GetAgentStatus(ctx context.Context, id uuid.UUID) (sextantproto.AgentStatus, error) {
	var resp sextantproto.GetAgentStatusResponse
	// IncludeHeartbeat asks the daemon to attach its HeartbeatCache
	// reading so runAgentCheck can issue the `degraded` verdict when
	// lifecycle=running but the sidecar hasn't checked in. The daemon
	// silently omits the field on older builds (json: omitempty), so
	// older daemons return today's verdict set.
	if err := c.cli.RPC(ctx, rpc.VerbGetAgentStatus,
		sextantproto.GetAgentStatusRequest{AgentID: id, IncludeHeartbeat: true}, &resp); err != nil {
		return sextantproto.AgentStatus{}, err
	}
	return resp.Status, nil
}

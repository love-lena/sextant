// agents.go owns the `sextant agents <verb>` command tree. Includes
// chat (replacing both `sextant conversation` and `sextant ask`) and
// exec (relocated from top-level per `feat-cli-resource-verb-cleanup`).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// newAgentsCmd builds the `sextant agents` parent command and registers
// every verb under it. Verbs:
//
//	list, show, spawn, kill, restart, archive, prompt, chat, exec.
func newAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Agent operations",
		Long: `Spawn, inspect, prompt, and control agents managed by sextantd.

Common verbs: list, show, spawn, kill, restart, archive, prompt, chat,
exec.`,
	}
	cmd.AddCommand(newAgentsListCmd())
	cmd.AddCommand(newAgentsShowCmd())
	cmd.AddCommand(newAgentsSpawnCmd())
	cmd.AddCommand(newAgentsKillCmd())
	cmd.AddCommand(newAgentsRestartCmd())
	cmd.AddCommand(newAgentsArchiveCmd())
	cmd.AddCommand(newAgentsPromptCmd())
	cmd.AddCommand(newAgentsChatCmd())
	cmd.AddCommand(newAgentsExecCmd())
	cmd.AddCommand(newAgentsCheckCmd())
	return cmd
}

// connectAgent builds a live pkg/client.Client against the running
// daemon. It loads sextantd.toml + runtime.json + operator.creds so
// the connection lands on the auto-allocated NATS port the daemon
// records on first boot — the client.toml default port 4222 is a
// placeholder.
func connectAgent(ctx context.Context, configDir string) (*client.Client, sextantd.RuntimeInfo, error) {
	if configDir == "" {
		d, _, err := sextantd.DefaultPaths()
		if err != nil {
			return nil, sextantd.RuntimeInfo{}, err
		}
		configDir = d
	}
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	sd, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		return nil, sextantd.RuntimeInfo{}, fmt.Errorf("load sextantd.toml: %w", err)
	}
	rt, err := sextantd.ReadRuntimeInfo(sd.Paths.RuntimeFile)
	if err != nil {
		return nil, sextantd.RuntimeInfo{}, fmt.Errorf("read runtime.json: %w (is sextantd running?)", err)
	}
	creds, err := sextantd.ReadOperatorCreds(sd.NATS.OperatorCreds)
	if err != nil {
		return nil, sextantd.RuntimeInfo{}, fmt.Errorf("read operator creds: %w", err)
	}
	clientCfg := client.Config{
		NATS:     client.NATSConfig{URL: "nats://" + rt.NATSAddr},
		Operator: client.OperatorConfig{User: creds.User, Password: creds.Password},
		Client:   client.ClientConfig{ConnectTimeout: client.Duration(10 * time.Second), RequestTimeout: client.Duration(30 * time.Second)},
	}
	cli, err := client.ConnectWithConfig(ctx, clientCfg)
	if err != nil {
		return nil, sextantd.RuntimeInfo{}, fmt.Errorf("connect: %w", err)
	}
	return cli, rt, nil
}

// newAgentsListCmd — `sextant agents list`.
func newAgentsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List known agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			var resp sextantproto.ListAgentsResponse
			if err := cli.RPC(ctx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
				return fmt.Errorf("list_agents: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp)
			}
			if len(resp.Agents) == 0 {
				_, err := fmt.Fprintln(out, "no agents")
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "UUID\tNAME\tTEMPLATE\tLIFECYCLE\tVERSION\tUPDATED")
			for _, a := range resp.Agents {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
					a.UUID, a.Name, a.Template, a.Lifecycle, a.Version,
					a.UpdatedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
}

// newAgentsShowCmd — `sextant agents show <agent>`.
func newAgentsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <agent>",
		Short: "Detailed status for one agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			id, err := resolveAgentRef(ctx, cli, args[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("agent: %v", err))
			}

			var resp sextantproto.GetAgentStatusResponse
			if err := cli.RPC(ctx, rpc.VerbGetAgentStatus,
				sextantproto.GetAgentStatusRequest{AgentID: id}, &resp); err != nil {
				return fmt.Errorf("get_agent_status: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp.Status)
			}
			fmt.Fprintf(out, "UUID:      %s\n", resp.Status.UUID)
			fmt.Fprintf(out, "Name:      %s\n", resp.Status.Name)
			fmt.Fprintf(out, "Lifecycle: %s\n", resp.Status.Lifecycle)
			fmt.Fprintf(out, "Version:   %d\n", resp.Status.Version)
			fmt.Fprintf(out, "Updated:   %s\n", resp.Status.UpdatedAt.Format(time.RFC3339))
			return nil
		},
	}
}

// newAgentsSpawnCmd — `sextant agents spawn <name> --template T`.
func newAgentsSpawnCmd() *cobra.Command {
	var template, host string
	cmd := &cobra.Command{
		Use:   "spawn <name>",
		Short: "Create + start a new agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return errUserUsage(`sextant agents spawn <name> --template T [--host H]`)
			}
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			req := sextantproto.SpawnAgentRequest{
				Name:     args[0],
				Template: template,
				HostPin:  host,
			}
			var resp sextantproto.SpawnAgentResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbSpawnAgent, req, &resp); err != nil {
				return fmt.Errorf("spawn_agent: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp)
			}
			fmt.Fprintf(out, "agent_id: %s\n", resp.AgentID)
			return nil
		},
	}
	cmd.Flags().StringVar(&template, "template", "default",
		"template name (see ~/.config/sextant/templates/)")
	cmd.Flags().StringVar(&host, "host", "", "host pin (optional)")
	return cmd
}

// newAgentsKillCmd — `sextant agents kill <agent> [--grace 10s] [--archive]`.
//
// The `--archive` flag pairs the kill with an archive_agent RPC against
// the same UUID so the agent's name is released back into the
// uniqueness pool immediately. Without it the agent stays in
// lifecycle=defined and its name remains claimed — see
// plans/issues/bug-kill-doesnt-release-name.md.
func newAgentsKillCmd() *cobra.Command {
	var grace time.Duration
	var archive bool
	cmd := &cobra.Command{
		Use:   "kill <agent>",
		Short: "Terminate a running agent",
		Args:  cobra.ExactArgs(1),
	}
	destructive := newDestructiveFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, _, err := connectAgent(ctx, globalFlags.configDir)
		if err != nil {
			return err
		}
		defer cli.Close() //nolint:errcheck // best-effort close

		id, err := resolveAgentRef(ctx, cli, args[0])
		if err != nil {
			return errUserUsage(fmt.Sprintf("agent: %v", err))
		}

		action := fmt.Sprintf("kill agent %s (%s)", args[0], id)
		if archive {
			action = fmt.Sprintf("kill + archive agent %s (%s)", args[0], id)
		}
		proceed, err := destructive.confirm(cmd, action)
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}

		req := sextantproto.KillAgentRequest{
			AgentID:      id,
			GraceSeconds: int(grace / time.Second),
		}
		rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		var resp sextantproto.KillAgentResponse
		if err := cli.RPC(rpcCtx, rpc.VerbKillAgent, req, &resp); err != nil {
			return fmt.Errorf("kill_agent: %w", err)
		}
		if archive {
			archiveCtx, cancelArchive := context.WithTimeout(ctx, 60*time.Second)
			var archiveResp sextantproto.ArchiveAgentResponse
			archiveErr := cli.RPC(archiveCtx, rpc.VerbArchiveAgent,
				sextantproto.ArchiveAgentRequest{AgentID: id},
				&archiveResp)
			cancelArchive()
			if archiveErr != nil {
				return fmt.Errorf("kill ok but archive failed: %w", archiveErr)
			}
			if !archiveResp.OK {
				return fmt.Errorf("kill ok but archive returned ok=false")
			}
		}
		out := cmd.OutOrStdout()
		if globalFlags.asJSON {
			return writeJSON(out, resp)
		}
		if resp.OK {
			_, err = fmt.Fprintln(out, "ok")
		} else {
			_, err = fmt.Fprintln(out, "not ok")
		}
		return err
	}
	cmd.Flags().DurationVar(&grace, "grace", 10*time.Second,
		"graceful stop deadline before SIGKILL")
	cmd.Flags().BoolVar(&archive, "archive", false,
		"archive the agent after the kill so its name is reusable")
	return cmd
}

// newAgentsRestartCmd — `sextant agents restart <agent> [--preserve-session]`.
func newAgentsRestartCmd() *cobra.Command {
	var preserve bool
	cmd := &cobra.Command{
		Use:   "restart <agent>",
		Short: "Restart a running agent in place",
		Args:  cobra.ExactArgs(1),
	}
	destructive := newDestructiveFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, _, err := connectAgent(ctx, globalFlags.configDir)
		if err != nil {
			return err
		}
		defer cli.Close() //nolint:errcheck // best-effort close

		id, err := resolveAgentRef(ctx, cli, args[0])
		if err != nil {
			return errUserUsage(fmt.Sprintf("agent: %v", err))
		}

		proceed, err := destructive.confirm(cmd, fmt.Sprintf("restart agent %s (%s)", args[0], id))
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}

		req := sextantproto.RestartAgentRequest{
			AgentID:         id,
			PreserveSession: preserve,
		}
		var resp sextantproto.RestartAgentResponse
		rpcCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		if err := cli.RPC(rpcCtx, rpc.VerbRestartAgent, req, &resp); err != nil {
			return fmt.Errorf("restart_agent: %w", err)
		}
		out := cmd.OutOrStdout()
		if globalFlags.asJSON {
			return writeJSON(out, resp)
		}
		if resp.OK {
			_, err = fmt.Fprintf(out, "agent_id: %s\n", resp.AgentID)
		} else {
			_, err = fmt.Fprintln(out, "not ok")
		}
		return err
	}
	cmd.Flags().BoolVar(&preserve, "preserve-session", false,
		"preserve session state across the restart (reserved; no-op today)")
	return cmd
}

// newAgentsArchiveCmd — `sextant agents archive <agent> | --all-dead`.
//
// Archive flips the agent's lifecycle to "archived", the only state that
// releases the agent's name back into the uniqueness pool.
// `--all-dead` archives every lifecycle=defined agent in one call.
func newAgentsArchiveCmd() *cobra.Command {
	var allDead bool
	cmd := &cobra.Command{
		Use:   "archive <agent>",
		Short: "Mark the agent archived so its name is released",
		Long: `Flips the agent's lifecycle to "archived". Without --all-dead, requires
exactly one agent reference (UUID or name). --all-dead bulk-archives every
agent currently in lifecycle=defined.`,
		Args: cobra.MaximumNArgs(1),
	}
	destructive := newDestructiveFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, _, err := connectAgent(ctx, globalFlags.configDir)
		if err != nil {
			return err
		}
		defer cli.Close() //nolint:errcheck // best-effort close

		if allDead {
			if len(args) != 0 {
				return errUserUsage("sextant agents archive --all-dead takes no positional args")
			}
			proceed, err := destructive.confirm(cmd, "archive every agent in lifecycle=defined")
			if err != nil {
				return err
			}
			if !proceed {
				return nil
			}
			return runAgentsArchiveAllDead(ctx, cmd.OutOrStdout(), cli)
		}
		if len(args) != 1 {
			return errUserUsage("sextant agents archive <agent> | --all-dead")
		}
		id, err := resolveAgentRef(ctx, cli, args[0])
		if err != nil {
			return errUserUsage(fmt.Sprintf("agent: %v", err))
		}
		proceed, err := destructive.confirm(cmd, fmt.Sprintf("archive agent %s (%s)", args[0], id))
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
		rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		var resp sextantproto.ArchiveAgentResponse
		if err := cli.RPC(rpcCtx, rpc.VerbArchiveAgent,
			sextantproto.ArchiveAgentRequest{AgentID: id}, &resp); err != nil {
			return fmt.Errorf("archive_agent: %w", err)
		}
		out := cmd.OutOrStdout()
		if globalFlags.asJSON {
			return writeJSON(out, resp)
		}
		if resp.OK {
			_, err = fmt.Fprintln(out, "ok")
		} else {
			_, err = fmt.Fprintln(out, "not ok")
		}
		return err
	}
	cmd.Flags().BoolVar(&allDead, "all-dead", false,
		"archive every agent currently in lifecycle defined")
	return cmd
}

// runAgentsArchiveAllDead lists every agent in lifecycle=defined and
// issues an archive_agent RPC for each. Failures on individual agents
// are logged but don't abort the loop.
func runAgentsArchiveAllDead(ctx context.Context, out io.Writer, cli *client.Client) error {
	var listResp sextantproto.ListAgentsResponse
	listCtx, listCancel := context.WithTimeout(ctx, 30*time.Second)
	err := cli.RPC(listCtx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{
		Filter: &sextantproto.ListAgentsFilter{Lifecycle: string(sextantproto.LifecycleDefined)},
	}, &listResp)
	listCancel()
	if err != nil {
		return fmt.Errorf("list_agents: %w", err)
	}
	type result struct {
		UUID  uuid.UUID `json:"uuid"`
		Name  string    `json:"name"`
		OK    bool      `json:"ok"`
		Error string    `json:"error,omitempty"`
	}
	results := make([]result, 0, len(listResp.Agents))
	for _, a := range listResp.Agents {
		archCtx, archCancel := context.WithTimeout(ctx, 60*time.Second)
		var archResp sextantproto.ArchiveAgentResponse
		archErr := cli.RPC(archCtx, rpc.VerbArchiveAgent,
			sextantproto.ArchiveAgentRequest{AgentID: a.UUID}, &archResp)
		archCancel()
		r := result{UUID: a.UUID, Name: a.Name, OK: archErr == nil && archResp.OK}
		if archErr != nil {
			r.Error = archErr.Error()
		}
		results = append(results, r)
	}
	if globalFlags.asJSON {
		return writeJSON(out, results)
	}
	if len(results) == 0 {
		_, err := fmt.Fprintln(out, "no defined agents to archive")
		return err
	}
	for _, r := range results {
		if r.OK {
			fmt.Fprintf(out, "archived %s (%s)\n", r.Name, r.UUID)
		} else {
			fmt.Fprintf(out, "FAILED  %s (%s): %s\n", r.Name, r.UUID, r.Error)
		}
	}
	return nil
}

// resolveAgentRef accepts either a UUID string or an agent name. When
// `ref` parses as a UUID we use it directly; otherwise we look the name
// up via list_agents (filtering out archived entries, since their names
// are released and may legally collide with a freshly-spawned
// non-archived agent). Returns the matching UUID, or an error if zero
// or multiple non-archived agents share the name.
func resolveAgentRef(ctx context.Context, cli *client.Client, ref string) (uuid.UUID, error) {
	return resolveAgentRefWithLister(ref, func() ([]sextantproto.AgentSummary, error) {
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var resp sextantproto.ListAgentsResponse
		if err := cli.RPC(listCtx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
			return nil, fmt.Errorf("list_agents: %w", err)
		}
		return resp.Agents, nil
	})
}

// resolveAgentRefWithLister is the side-effect-free core of
// resolveAgentRef. Callers inject `lister` to supply the agent inventory.
func resolveAgentRefWithLister(ref string, lister func() ([]sextantproto.AgentSummary, error)) (uuid.UUID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return id, nil
	}
	agents, err := lister()
	if err != nil {
		return uuid.Nil, err
	}
	var matches []sextantproto.AgentSummary
	for _, a := range agents {
		if a.Name == ref && a.Lifecycle != string(sextantproto.LifecycleArchived) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		return uuid.Nil, fmt.Errorf("no non-archived agent named %q", ref)
	case 1:
		return matches[0].UUID, nil
	default:
		uuids := make([]string, 0, len(matches))
		for _, m := range matches {
			uuids = append(uuids, m.UUID.String())
		}
		return uuid.Nil, fmt.Errorf("multiple non-archived agents named %q: %s", ref, strings.Join(uuids, ", "))
	}
}

// newAgentsPromptCmd — `sextant agents prompt <agent> "<text>"`.
func newAgentsPromptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt <agent> <text>",
		Short: "Send a prompt to an agent's inbox",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			id, err := resolveAgentRef(ctx, cli, args[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("agent: %v", err))
			}
			req := sextantproto.PromptAgentRequest{
				AgentID: id,
				Content: args[1],
			}
			var resp sextantproto.PromptAgentResponse
			if err := cli.RPC(ctx, rpc.VerbPromptAgent, req, &resp); err != nil {
				return fmt.Errorf("prompt_agent: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp)
			}
			if resp.OK {
				_, err = fmt.Fprintln(out, "ok")
			}
			return err
		},
	}
}

// writeJSON pretty-prints v to w with a trailing newline.
func writeJSON(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}

// ensureNotEmpty is a tiny helper for usage-error reporting.
//
//nolint:unused // reserved for future verbs
func ensureNotEmpty(label, v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New(label + " is required")
	}
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

const agentsUsage = `usage: sextant agents <verb> [args...]

Verbs:
  list                                List known agents.
  show <agent>                        Detailed status for one agent.
  spawn <name> --template T           Create + start a new agent.
  kill <agent> [--grace 10s]          Stop a running agent.
  prompt <agent> "<text>"             Send a prompt to an agent's inbox.

Every verb supports --json for machine-parseable output. Use
--config-dir to point at a non-default sextant install.`

// runAgents dispatches the second-level verb (list/show/spawn/kill/prompt).
func runAgents(ctx context.Context, args []string) error {
	if len(args) == 0 {
		println(os.Stderr, agentsUsage)
		return errUserUsage("missing agents verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		return runAgentsList(ctx, rest)
	case "show":
		return runAgentsShow(ctx, rest)
	case "spawn":
		return runAgentsSpawn(ctx, rest)
	case "kill":
		return runAgentsKill(ctx, rest)
	case "prompt":
		return runAgentsPrompt(ctx, rest)
	case "-h", "--help", "help":
		println(os.Stdout, agentsUsage)
		return nil
	default:
		println(os.Stderr, agentsUsage)
		return errUserUsage(fmt.Sprintf("unknown agents verb %q", verb))
	}
}

// commonOpts holds flags shared by every agents verb.
type commonOpts struct {
	configDir string
	asJSON    bool
}

func parseCommonOpts(fs *flag.FlagSet, args []string) (commonOpts, []string, error) {
	var o commonOpts
	fs.StringVar(&o.configDir, "config-dir", "", "config directory (default ~/.config/sextant)")
	fs.BoolVar(&o.asJSON, "json", false, "emit machine-parseable JSON")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return o, nil, errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	return o, fs.Args(), nil
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

// runAgentsList — `sextant agents list`.
func runAgentsList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents list", flag.ContinueOnError)
	opts, _, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	var resp sextantproto.ListAgentsResponse
	if err := cli.RPC(ctx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
		return fmt.Errorf("list_agents: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if len(resp.Agents) == 0 {
		println(os.Stdout, "no agents")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "UUID\tNAME\tTEMPLATE\tLIFECYCLE\tVERSION\tUPDATED")
	for _, a := range resp.Agents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			a.UUID, a.Name, a.Template, a.Lifecycle, a.Version,
			a.UpdatedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

// runAgentsShow — `sextant agents show <agent>`.
func runAgentsShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents show", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant agents show <agent_uuid>")
	}
	id, err := uuid.Parse(rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent_uuid: %v", err))
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	var resp sextantproto.GetAgentStatusResponse
	if err := cli.RPC(ctx, rpc.VerbGetAgentStatus, sextantproto.GetAgentStatusRequest{AgentID: id}, &resp); err != nil {
		return fmt.Errorf("get_agent_status: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp.Status)
	}
	printf(os.Stdout, "UUID:      %s\n", resp.Status.UUID)
	printf(os.Stdout, "Name:      %s\n", resp.Status.Name)
	printf(os.Stdout, "Lifecycle: %s\n", resp.Status.Lifecycle)
	printf(os.Stdout, "Version:   %d\n", resp.Status.Version)
	printf(os.Stdout, "Updated:   %s\n", resp.Status.UpdatedAt.Format(time.RFC3339))
	return nil
}

// runAgentsSpawn — `sextant agents spawn <name> --template T`.
func runAgentsSpawn(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents spawn", flag.ContinueOnError)
	var template, host string
	fs.StringVar(&template, "template", "default", "template name (see ~/.config/sextant/templates/)")
	fs.StringVar(&host, "host", "", "host pin (optional)")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return errUserUsage(`sextant agents spawn <name> --template T [--host H]`)
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	req := sextantproto.SpawnAgentRequest{
		Name:     rest[0],
		Template: template,
		HostPin:  host,
	}
	var resp sextantproto.SpawnAgentResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbSpawnAgent, req, &resp); err != nil {
		return fmt.Errorf("spawn_agent: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	printf(os.Stdout, "agent_id: %s\n", resp.AgentID)
	return nil
}

// runAgentsKill — `sextant agents kill <agent>`.
func runAgentsKill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents kill", flag.ContinueOnError)
	var grace time.Duration
	fs.DurationVar(&grace, "grace", 10*time.Second, "graceful stop deadline before SIGKILL")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant agents kill <agent_uuid>")
	}
	id, err := uuid.Parse(rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent_uuid: %v", err))
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

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
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if resp.OK {
		println(os.Stdout, "ok")
	} else {
		println(os.Stdout, "not ok")
	}
	return nil
}

// runAgentsPrompt — `sextant agents prompt <agent> "<text>"`.
func runAgentsPrompt(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents prompt", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errUserUsage(`sextant agents prompt <agent_uuid> "<text>"`)
	}
	id, err := uuid.Parse(rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent_uuid: %v", err))
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	req := sextantproto.PromptAgentRequest{
		AgentID: id,
		Content: rest[1],
	}
	var resp sextantproto.PromptAgentResponse
	if err := cli.RPC(ctx, rpc.VerbPromptAgent, req, &resp); err != nil {
		return fmt.Errorf("prompt_agent: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if resp.OK {
		println(os.Stdout, "ok")
	}
	return nil
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
//nolint:unused // reserved for future verbs (`agents archive`, etc.)
func ensureNotEmpty(label, v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New(label + " is required")
	}
	return nil
}

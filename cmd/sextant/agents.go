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

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

const agentsUsage = `usage: sextant agents <verb> [args...]

Verbs:
  list                                List known agents.
  show <agent>                        Detailed status for one agent.
  spawn <name> --template T           Create + start a new agent.
  kill <agent> [--grace 10s] [--archive]
                                      Stop a running agent. --archive flips
                                      lifecycle to archived after the kill
                                      so the agent's name is reusable.
  restart <agent> [--preserve-session]
                                      Restart a running agent in place.
  archive <agent>                     Mark the agent archived so its name
                                      is released. --all-dead archives
                                      every agent currently in lifecycle
                                      "defined" (bulk cleanup).
  prompt <agent> "<text>"             Send a prompt to an agent's inbox.

Every verb supports --json for machine-parseable output. Use
--config-dir to point at a non-default sextant install.`

// runAgents dispatches the second-level verb (list/show/spawn/kill/prompt).
//
// Uses fmt.Fprintln directly rather than the package's `println` helper
// (output.go) because the helper shadows Go's builtin println — a casual
// reader can't tell at a glance which one is in scope. Explicit
// fmt.Fprintln(os.Stderr, ...) removes the ambiguity at the dispatch
// site where it matters most.
func runAgents(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, agentsUsage)
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
	case "restart":
		return runAgentsRestart(ctx, rest)
	case "archive":
		return runAgentsArchive(ctx, rest)
	case "prompt":
		return runAgentsPrompt(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, agentsUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, agentsUsage)
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
	// Go's stdlib flag stops at the first non-flag arg. The spec's verb
	// shape is `sextant agents spawn <name> --template T`, so we
	// shuffle every flag (and its value) to the front before parsing.
	// reorderFlagsBeforePositional walks the registered FlagSet to know
	// which flags expect a value vs. which are booleans.
	args = reorderFlagsBeforePositional(fs, args)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// --help/-h: caller's fs.Usage (or our wrapper around it) has
			// already printed the usage. Pass ErrHelp up unwrapped so
			// main.go can exit cleanly (code 0) instead of treating it
			// as a parse failure.
			return o, nil, err
		}
		return o, nil, errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	return o, fs.Args(), nil
}

// reorderFlagsBeforePositional moves every flag (and its value when
// the flag is not boolean) ahead of every positional arg so the stdlib
// flag parser sees them all before stopping. Honors `--` to opt out.
//
// The fs is used to look up registered flags so bool flags don't
// accidentally consume the next token as a value.
func reorderFlagsBeforePositional(fs *flag.FlagSet, args []string) []string {
	isBool := func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		bf, ok := f.Value.(interface{ IsBoolFlag() bool })
		return ok && bf.IsBoolFlag()
	}
	var flags, positional []string
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			positional = append(positional, args[i:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && a != "-":
			flags = append(flags, a)
			// "--foo=bar" → already self-contained.
			// "--foo bar" → grab the next token unless --foo is a bool
			// flag (in which case the next token is positional).
			if !strings.Contains(a, "=") && i+1 < len(args) {
				name := strings.TrimLeft(a, "-")
				if !isBool(name) {
					flags = append(flags, args[i+1])
					i++
				}
			}
			i++
		default:
			positional = append(positional, a)
			i++
		}
	}
	return append(flags, positional...)
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
	printf(tw, "UUID\tNAME\tTEMPLATE\tLIFECYCLE\tVERSION\tUPDATED\n")
	for _, a := range resp.Agents {
		printf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
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
		return errUserUsage("sextant agents show <agent>")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	id, err := resolveAgentRef(ctx, cli, rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent: %v", err))
	}

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

// runAgentsKill — `sextant agents kill <agent> [--archive]`.
//
// The `--archive` flag pairs the kill with an archive_agent RPC against
// the same UUID so the agent's name is released back into the
// uniqueness pool immediately. Without it the agent stays in
// lifecycle=defined and its name remains claimed — see
// plans/issues/bug-kill-doesnt-release-name.md.
func runAgentsKill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents kill", flag.ContinueOnError)
	var grace time.Duration
	var archive bool
	fs.DurationVar(&grace, "grace", 10*time.Second, "graceful stop deadline before SIGKILL")
	fs.BoolVar(&archive, "archive", false, "archive the agent after the kill so its name is reusable")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant agents kill <agent>")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	id, err := resolveAgentRef(ctx, cli, rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent: %v", err))
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
		// Best-effort archive: a failure here doesn't undo the kill, so
		// we surface it as a wrapped error rather than panicking. The
		// caller can re-run `sextant agents archive` if this leg
		// stumbles.
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

// runAgentsRestart — `sextant agents restart <agent> [--preserve-session]`.
func runAgentsRestart(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents restart", flag.ContinueOnError)
	var preserve bool
	fs.BoolVar(&preserve, "preserve-session", false, "preserve session state across the restart (reserved; no-op today)")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant agents restart <agent> [--preserve-session]")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	id, err := resolveAgentRef(ctx, cli, rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent: %v", err))
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
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if resp.OK {
		printf(os.Stdout, "agent_id: %s\n", resp.AgentID)
	} else {
		println(os.Stdout, "not ok")
	}
	return nil
}

// runAgentsArchive — `sextant agents archive <agent> | --all-dead`.
//
// Archive flips the agent's lifecycle to "archived", the only state per
// architecture.md §2 that releases the agent's name back into the
// uniqueness pool. Without this verb, an agent killed via
// `sextant agents kill` stays in lifecycle=defined forever and its name
// is permanently claimed; see
// plans/issues/feat-agents-archive-cli-verb.md.
//
// `--all-dead` archives every agent currently in lifecycle "defined" in
// one call so an operator can clean up after a smoke run without
// listing UUIDs by hand.
func runAgentsArchive(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents archive", flag.ContinueOnError)
	var allDead bool
	fs.BoolVar(&allDead, "all-dead", false, "archive every agent currently in lifecycle defined")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	if allDead {
		if len(rest) != 0 {
			return errUserUsage("sextant agents archive --all-dead takes no positional args")
		}
		return runAgentsArchiveAllDead(ctx, cli, opts)
	}
	if len(rest) != 1 {
		return errUserUsage("sextant agents archive <agent> | --all-dead")
	}
	id, err := resolveAgentRef(ctx, cli, rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent: %v", err))
	}

	rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var resp sextantproto.ArchiveAgentResponse
	if err := cli.RPC(rpcCtx, rpc.VerbArchiveAgent,
		sextantproto.ArchiveAgentRequest{AgentID: id}, &resp); err != nil {
		return fmt.Errorf("archive_agent: %w", err)
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

// runAgentsArchiveAllDead lists every agent in lifecycle=defined and
// issues an archive_agent RPC for each. Failures on individual agents
// are logged but don't abort the loop — the bulk cleanup is meant to
// run after a smoke test where some entries may already be in odd
// states.
func runAgentsArchiveAllDead(ctx context.Context, cli *client.Client, opts commonOpts) error {
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
	out := make([]result, 0, len(listResp.Agents))
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
		out = append(out, r)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, out)
	}
	if len(out) == 0 {
		println(os.Stdout, "no defined agents to archive")
		return nil
	}
	for _, r := range out {
		if r.OK {
			printf(os.Stdout, "archived %s (%s)\n", r.Name, r.UUID)
		} else {
			printf(os.Stdout, "FAILED  %s (%s): %s\n", r.Name, r.UUID, r.Error)
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
//
// Wired into every `agents <verb>` that takes an `<agent>` positional
// arg (list, show, kill, restart, archive, prompt) so the surface is
// uniform — operators can pass either UUID or name to any verb. This
// fixes bug-name-resolution-inconsistent-across-agents-verbs.md:
// pre-fix, only `archive` accepted names while `prompt`/`show`/etc.
// errored with `invalid UUID length`.
//
// The actual matching/disambiguation lives in resolveAgentRefWithLister
// so tests can drive it without spinning up a NATS server.
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
// resolveAgentRef. Callers inject `lister` to supply the agent inventory
// — the prod wiring calls list_agents over NATS; tests pass a closure
// returning a hand-built slice.
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
		// Shouldn't happen under the uniqueness invariant, but surface
		// the ambiguity instead of picking arbitrarily.
		uuids := make([]string, 0, len(matches))
		for _, m := range matches {
			uuids = append(uuids, m.UUID.String())
		}
		return uuid.Nil, fmt.Errorf("multiple non-archived agents named %q: %s", ref, strings.Join(uuids, ", "))
	}
}

// runAgentsPrompt — `sextant agents prompt <agent> "<text>"`.
func runAgentsPrompt(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant agents prompt", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errUserUsage(`sextant agents prompt <agent> "<text>"`)
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	id, err := resolveAgentRef(ctx, cli, rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent: %v", err))
	}

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
//nolint:unused // reserved for future verbs
func ensureNotEmpty(label, v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New(label + " is required")
	}
	return nil
}

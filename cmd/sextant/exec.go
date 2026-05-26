package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

const execUsage = `usage: sextant exec <agent_uuid> [--workdir DIR] [--env K=V]... -- <cmd> [args...]

Run a command inside the agent's container. Capability-gated as
control.exec (operator-level). Returns stdout to stdout, stderr to
stderr, and exits with the command's exit code so shell pipelines
behave naturally.

--json emits the full ExecInContainerResponse as JSON to stdout
instead of streaming.`

// envFlag is a repeatable --env flag (--env K=V --env A=B).
type envFlag struct {
	pairs map[string]string
}

func (e *envFlag) String() string { return fmt.Sprintf("%v", e.pairs) }
func (e *envFlag) Set(v string) error {
	if e.pairs == nil {
		e.pairs = map[string]string{}
	}
	// Split at the first '=' so values can contain '='.
	for i := 0; i < len(v); i++ {
		if v[i] == '=' {
			e.pairs[v[:i]] = v[i+1:]
			return nil
		}
	}
	return fmt.Errorf("--env requires K=V form, got %q", v)
}

// runExec — `sextant exec <agent_uuid> -- cmd args...`
func runExec(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant exec", flag.ContinueOnError)
	var workdir string
	envs := &envFlag{}
	fs.StringVar(&workdir, "workdir", "", "working directory inside the container")
	fs.Var(envs, "env", "K=V env vars (repeat for multiple)")

	// The exec verb's positional shape is `<agent> -- <cmd>...`. We
	// split args at the first `--` so the command's own flags (e.g.
	// `ls -lah`) don't get parsed by our FlagSet.
	flagArgs, cmdArgs := splitDoubleDash(args)
	opts, rest, err := parseCommonOpts(fs, flagArgs)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintln(os.Stderr, execUsage)
		return errUserUsage("sextant exec <agent_uuid> -- <cmd> [args...]")
	}
	if len(cmdArgs) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, execUsage)
		return errUserUsage("missing command after --")
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

	req := sextantproto.ExecInContainerRequest{
		AgentID: id,
		Cmd:     cmdArgs,
		Workdir: workdir,
		Env:     envs.pairs,
	}
	var resp sextantproto.ExecInContainerResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbExecInContainer, req, &resp); err != nil {
		return fmt.Errorf("exec_in_container: %w", err)
	}
	if opts.asJSON {
		if err := writeJSON(os.Stdout, resp); err != nil {
			return err
		}
		if resp.ExitCode != 0 {
			return &exitCodeError{code: resp.ExitCode}
		}
		return nil
	}
	// Mirror docker exec: stdout to stdout, stderr to stderr, exit
	// code becomes the CLI's exit.
	if _, err := os.Stdout.WriteString(resp.Stdout); err != nil {
		return err
	}
	if _, err := os.Stderr.WriteString(resp.Stderr); err != nil {
		return err
	}
	if resp.ExitCode != 0 {
		return &exitCodeError{code: resp.ExitCode}
	}
	return nil
}

// exitCodeError carries the container exec's exit code so the CLI can
// surface it via os.Exit. errors.As-able from main's exitCodeFor.
type exitCodeError struct {
	code int
}

func (e *exitCodeError) Error() string {
	return fmt.Sprintf("command exited with code %d", e.code)
}

// splitDoubleDash partitions args at the first standalone `--`. The
// `--` itself is dropped.
func splitDoubleDash(args []string) (before, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

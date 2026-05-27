// exec.go owns `sextant agents exec <agent> -- <cmd>`. Relocated under
// the `agents` resource from the previous top-level `sextant exec` per
// `plans/issues/feat-cli-resource-verb-cleanup.md`. The legacy form
// stays as an alias for one minor release.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// envFlag is a repeatable --env flag (--env K=V --env A=B).
type envFlag struct {
	pairs map[string]string
}

func (e *envFlag) String() string { return fmt.Sprintf("%v", e.pairs) }
func (e *envFlag) Type() string   { return "K=V" }
func (e *envFlag) Set(v string) error {
	if e.pairs == nil {
		e.pairs = map[string]string{}
	}
	for i := 0; i < len(v); i++ {
		if v[i] == '=' {
			e.pairs[v[:i]] = v[i+1:]
			return nil
		}
	}
	return fmt.Errorf("--env requires K=V form, got %q", v)
}

// newAgentsExecCmd wires `sextant agents exec <agent> -- <cmd> [args...]`.
// Capability-gated as control.exec (operator-level). Mirrors docker exec:
// stdout → stdout, stderr → stderr, container exit code → CLI exit code.
func newAgentsExecCmd() *cobra.Command {
	var workdir string
	envs := &envFlag{}
	cmd := &cobra.Command{
		Use:   "exec <agent_uuid> -- <cmd> [args...]",
		Short: "Run a command inside an agent's container",
		Long: `Capability-gated (control.exec) command execution inside an agent's
container. Output mirrors docker exec: stdout → stdout, stderr → stderr,
the command's exit code becomes the CLI's exit code so shell pipelines
behave naturally.

--json emits the full ExecInContainerResponse as JSON to stdout instead
of streaming.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 {
				return errUserUsage("sextant agents exec <agent_uuid> -- <cmd> [args...]")
			}
			cmdArgs := args[dash:]
			if len(cmdArgs) == 0 {
				return errUserUsage("missing command after --")
			}
			positional := args[:dash]
			if len(positional) != 1 {
				return errUserUsage("sextant agents exec <agent_uuid> -- <cmd> [args...]")
			}
			id, err := uuid.Parse(positional[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("agent_uuid: %v", err))
			}

			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
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
			if globalFlags.asJSON {
				if err := writeJSON(cmd, cmd.OutOrStdout(), resp); err != nil {
					return err
				}
				if resp.ExitCode != 0 {
					return &exitCodeError{code: resp.ExitCode}
				}
				return nil
			}
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
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "working directory inside the container")
	cmd.Flags().Var(envs, "env", "K=V env vars (repeat for multiple)")
	return cmd
}

// exitCodeError carries the container exec's exit code so the CLI can
// surface it via os.Exit. errors.As-able from mainErr's exitCodeFor.
type exitCodeError struct {
	code int
}

func (e *exitCodeError) Error() string {
	return fmt.Sprintf("command exited with code %d", e.code)
}

// splitDoubleDash partitions args at the first standalone `--`. The
// `--` itself is dropped. Kept for the exec_test.go helper-shape tests.
func splitDoubleDash(args []string) (before, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

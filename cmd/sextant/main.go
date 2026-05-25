// sextant is the operator CLI for the sextant initial deployment. M5
// ships two subcommands: `init` (first-run setup) and `doctor` (health
// diagnostics). Additional verbs (agents, conversation, files, ...) land
// in M11 and M12.
//
// Plan: plans/bootstrap.md#M5
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil {
		// `sextant exec` writes its own stdout/stderr verbatim; we
		// shouldn't add a "sextant: command exited with code N" line on
		// top — that would make shell pipelines noisy.
		var ec *exitCodeError
		if !errors.As(err, &ec) {
			printf(os.Stderr, "sextant: %v\n", err)
		}
		return exitCodeFor(err)
	}
	return exitOK
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return errUserUsage("missing subcommand")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return runInit(ctx, rest)
	case "doctor":
		return runDoctor(ctx, rest)
	case "agents":
		return runAgents(ctx, rest)
	case "conversation":
		return runConversation(ctx, rest)
	case "pending":
		return runPending(ctx, rest)
	case "files":
		return runFiles(ctx, rest)
	case "exec":
		return runExec(ctx, rest)
	case "audit":
		return runAudit(ctx, rest)
	case "tail":
		return runTail(ctx, rest)
	case "traces":
		return runTraces(ctx, rest)
	case "worktree":
		return runWorktree(ctx, rest)
	case "templates":
		return runTemplates(ctx, rest)
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return nil
	case "--version", "version":
		fmt.Println("sextant initial (M12)")
		return nil
	default:
		printUsage(os.Stderr)
		return errUserUsage(fmt.Sprintf("unknown subcommand %q", cmd))
	}
}

func printUsage(w *os.File) {
	println(w, `usage: sextant <subcommand> [args...]

Subcommands:
  init          First-run setup: CA + config + data dirs + default template.
  doctor        Health diagnostics for sextantd, NATS, ClickHouse, config.
  agents        Agent operations (list|show|spawn|kill|restart|prompt).
  conversation  Stream agent frames in human-readable form.
  pending       List/answer/defer/escalate user-input requests.
  files         Read/list/tail files in an agent's container.
  exec          Run a command in an agent's container.
  audit         Query or tail the audit log.
  tail          Subscribe to an arbitrary NATS subject (wildcards OK).
  traces        Render a distributed trace by trace_id.
  worktree      Manage agent worktrees (list|create|destroy|merge|diff).
  templates     Manage agent templates (reload).
  help          Print this message.
  version       Print the sextant version.

Run "sextant <subcommand> --help" for per-subcommand flags.`)
}

// Exit codes per specs/cli/commands.md.
const (
	exitOK     = 0
	exitUser   = 1
	exitSystem = 2
)

type usageError string

func (e usageError) Error() string { return string(e) }

func errUserUsage(msg string) error { return usageError(msg) }

func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	// `sextant exec` surfaces the container exec's exit code via
	// exitCodeError so shell pipelines see the same exit status they
	// would running the command directly.
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
	var ue usageError
	if errors.As(err, &ue) {
		return exitUser
	}
	// Bubble doctor's wrapped sentinel up.
	if isDoctorFailureErr(err) {
		return exitSystem
	}
	return exitSystem
}

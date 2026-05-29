// agents_context.go owns `sextant agents context <agent>` — the
// operator surface for inspecting an agent's Claude Code SDK session
// in its rawest practical form (the JSONL the SDK persists).
//
//   - dump the current session.jsonl to stdout once (default)
//   - --follow tails the file (nxadm/tail) until ctx cancellation
//   - --mode=<raw|conversation|tools|thinking|usage|tree> filters the
//     printed stream; raw is the floor
//   - -i / --tui opens the interactive tailing viewport (pkg/tui/contextview)
//
// The per-line rendering + mode vocabulary live in pkg/sessionlog
// (RenderLine / Mode / ParseMode) so the CLI dump and the `-i` TUI render
// identically.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nxadm/tail"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sessionlog"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// newAgentsContextCmd builds `sextant agents context <agent>`.
//
// The verb name `context` is on the closed-exception list in
// conventions/tui-conventions.md ("first-class operator concept").
func newAgentsContextCmd() *cobra.Command {
	var follow bool
	var interactive bool
	var modeFlag string
	cmd := &cobra.Command{
		Use:   "context <agent>",
		Short: "Print or tail the agent's Claude Code SDK session JSONL",
		Long: `Print the agent's Claude Code SDK session JSONL.

The session file is the rawest practical view of an agent's prompt
buffer — every assistant message, every tool call, every tool result,
the full token-usage breakdown per turn. The daemon bind-mounts the
file out of the container at spawn time so reads do not require
docker exec.

Defaults to one-shot dump (read file, print, exit). --follow turns it
into a tail -f. -i / --tui opens the interactive tailing viewport with
mode keys 1-6.

--mode=<raw|conversation|tools|thinking|usage|tree> filters the
stream:

  raw           verbatim JSONL lines (default; pipeable to jq)
  conversation  role-styled assistant/user text + tool blocks
  tools         only tool_use + matching tool_result records
  thinking      only assistant thinking content blocks
  usage         per-turn token totals + cache hit ratio
  tree          subagent tree grouped by parentUuid / isSidechain`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := sessionlog.ParseMode(modeFlag)
			if err != nil {
				return errUserUsage(err.Error())
			}
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
			if resp.Status.SessionLog == nil || resp.Status.SessionLog.ProjectsDir == "" {
				return fmt.Errorf("agent %s has no session log path on the daemon (daemon may predate agents-context bind mount, or agent was created on an older sextantd)", id)
			}
			if resp.Status.SessionLog.SessionID == "" {
				return fmt.Errorf("agent %s has not started a session yet (no SDK turn has completed; prompt the agent then retry)", id)
			}
			projectsDir := resp.Status.SessionLog.ProjectsDir
			sessionID := resp.Status.SessionLog.SessionID
			if interactive {
				_ = cli.Close()
				return activeTUILauncher.RunAgentsContext(ctx, globalFlags.configDir, projectsDir, sessionID)
			}
			jsonl, err := resolveSessionJSONLPath(projectsDir, sessionID)
			if err != nil {
				return err
			}
			return runAgentsContext(ctx, cmd.OutOrStdout(), jsonl, mode, follow)
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false, "tail the file (tail -f) until interrupted")
	cmd.Flags().BoolVarP(&interactive, "tui", "i", false, "open the interactive tailing viewport (mode keys 1-6)")
	cmd.Flags().StringVar(&modeFlag, "mode", string(sessionlog.ModeRaw),
		"view mode: raw|conversation|tools|thinking|usage|tree")
	return cmd
}

// resolveSessionJSONLPath locates the SDK session JSONL inside the
// per-agent claude-projects host directory. Layout per the Claude Code
// SDK: <projectsDir>/<encoded-cwd>/<sessionId>.jsonl. We walk the dir
// for any file matching <sessionId>.jsonl (one cwd → one subdir per
// agent), accepting both the per-cwd-subdir and direct-file shapes.
func resolveSessionJSONLPath(projectsDir, sessionID string) (string, error) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("read projects dir %s: %w", projectsDir, err)
	}
	target := sessionID + ".jsonl"
	for _, e := range entries {
		if !e.IsDir() {
			if e.Name() == target {
				return filepath.Join(projectsDir, e.Name()), nil
			}
			continue
		}
		candidate := filepath.Join(projectsDir, e.Name(), target)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("session.jsonl not found under %s for session %s (sidecar may not have flushed the first turn yet)", projectsDir, sessionID)
}

// runAgentsContext is the side-effect-free core. Accepts an opened file
// path (so tests can pass a tempfile), the view mode, and the follow
// flag. Returns when follow=false and EOF is reached, or follow=true and
// ctx is cancelled.
func runAgentsContext(ctx context.Context, out io.Writer, path string, mode sessionlog.Mode, follow bool) error {
	if !follow {
		f, err := os.Open(path) //nolint:gosec // operator-supplied agent ref → daemon-supplied session log path; not a file-inclusion vector
		if err != nil {
			return fmt.Errorf("open session log %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		return renderEvents(out, sessionlog.Stream(f), mode)
	}
	t, err := tail.TailFile(path, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: true,
		Logger:    tail.DiscardingLogger,
	})
	if err != nil {
		return fmt.Errorf("tail %s: %w", path, err)
	}
	defer func() { _ = t.Stop() }()

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-t.Lines:
				if !ok {
					return
				}
				if line.Err != nil {
					fmt.Fprintf(os.Stderr, "tail: %v\n", line.Err)
					return
				}
				if _, err := io.WriteString(pw, line.Text+"\n"); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		_ = t.Stop()
	}()
	return renderEvents(out, sessionlog.Stream(pr), mode)
}

// renderEvents pumps events into the writer using the shared per-line
// renderer. Returns nil on clean channel close.
func renderEvents(out io.Writer, ch <-chan sessionlog.Event, mode sessionlog.Mode) error {
	var acc *sessionlog.UsageAccumulator
	if mode == sessionlog.ModeUsage {
		acc = &sessionlog.UsageAccumulator{}
	}
	for ev := range ch {
		line := sessionlog.RenderLine(ev, mode, acc)
		if line == "" {
			continue
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		}
	}
	return nil
}

// runAgentsContextWithTimeout is a thin wrapper used by tests to bound
// the run; the production verb relies on the cobra command's ctx.
//
//nolint:unused // exercised via tests only
func runAgentsContextWithTimeout(parent context.Context, out io.Writer, path string, mode sessionlog.Mode, follow bool, d time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	return runAgentsContext(ctx, out, path, mode, follow)
}

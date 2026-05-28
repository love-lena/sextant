// agents_context.go owns `sextant agents context <agent>` — the
// operator surface for inspecting an agent's Claude Code SDK session
// in its rawest practical form (the JSONL the SDK persists).
//
// Phase A scope per plans/issues/feat-agents-context-view.md:
//
//   - dump the current session.jsonl to stdout once (default)
//   - --follow tails the file (nxadm/tail) until ctx cancellation
//   - --mode=<raw|conversation|tools|thinking|usage|tree> filters
//     the printed stream; raw is the floor
//
// The `-i` TUI mount is intentionally deferred — it depends on
// feat-cli-iflag-tier1-components.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nxadm/tail"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sessionlog"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// contextMode enumerates the --mode values the verb accepts. Raw is
// the floor (verbatim JSONL lines); the typed modes filter or
// reformat the stream.
type contextMode string

const (
	contextModeRaw          contextMode = "raw"
	contextModeConversation contextMode = "conversation"
	contextModeTools        contextMode = "tools"
	contextModeThinking     contextMode = "thinking"
	contextModeUsage        contextMode = "usage"
	contextModeTree         contextMode = "tree"
)

// allContextModes lists the valid --mode values in their canonical
// order. Used for the flag's usage string + validation.
var allContextModes = []contextMode{
	contextModeRaw,
	contextModeConversation,
	contextModeTools,
	contextModeThinking,
	contextModeUsage,
	contextModeTree,
}

// newAgentsContextCmd builds `sextant agents context <agent>`.
//
// The verb name `context` joins the closed-exception list in
// conventions/tui-conventions.md ("first-class operator concept" —
// inspecting the prompt buffer that drives a turn is not an `update`
// modifier on the agent record).
func newAgentsContextCmd() *cobra.Command {
	var follow bool
	var modeFlag string
	cmd := &cobra.Command{
		Use:   "context <agent>",
		Short: "Print the agent's Claude Code SDK session JSONL",
		Long: `Print the agent's Claude Code SDK session JSONL to stdout.

The session file is the rawest practical view of an agent's prompt
buffer — every assistant message, every tool call, every tool result,
the full token-usage breakdown per turn. The daemon bind-mounts the
file out of the container at spawn time so reads do not require
docker exec.

Defaults to one-shot dump (read file, print, exit). --follow turns it
into a tail -f.

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
			mode, err := parseContextMode(modeFlag)
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
			jsonl, err := resolveSessionJSONLPath(resp.Status.SessionLog.ProjectsDir, resp.Status.SessionLog.SessionID)
			if err != nil {
				return err
			}
			return runAgentsContext(ctx, cmd.OutOrStdout(), jsonl, mode, follow)
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false, "tail the file (tail -f) until interrupted")
	cmd.Flags().StringVar(&modeFlag, "mode", string(contextModeRaw),
		"view mode: raw|conversation|tools|thinking|usage|tree")
	return cmd
}

// parseContextMode validates the --mode value. Returns a usage error
// listing the legal values if v doesn't match.
func parseContextMode(v string) (contextMode, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return contextModeRaw, nil
	}
	for _, m := range allContextModes {
		if string(m) == v {
			return m, nil
		}
	}
	legal := make([]string, len(allContextModes))
	for i, m := range allContextModes {
		legal[i] = string(m)
	}
	return "", fmt.Errorf("invalid --mode %q (legal: %s)", v, strings.Join(legal, ", "))
}

// resolveSessionJSONLPath locates the SDK session JSONL inside the
// per-agent claude-projects host directory. Layout per the Claude
// Code SDK:
//
//	<projectsDir>/<encoded-cwd>/<sessionId>.jsonl
//
// The `<encoded-cwd>` segment is the SDK's URL-encoded representation
// of the in-container cwd — which the daemon doesn't need to mirror,
// so we walk the dir for any file matching `<sessionId>.jsonl`. In
// the bind-mounted layout there is exactly one such file per agent
// (only one cwd, only one current sessionId).
func resolveSessionJSONLPath(projectsDir, sessionID string) (string, error) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("read projects dir %s: %w", projectsDir, err)
	}
	target := sessionID + ".jsonl"
	for _, e := range entries {
		if !e.IsDir() {
			// Some SDK layouts write the file directly under projectsDir
			// (no per-cwd subdir). Accept that shape too — match on
			// the basename either way.
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

// runAgentsContext is the side-effect-free core. Accepts an opened
// file path (so tests can pass a tempfile), the view mode, and the
// follow flag. Writes filtered lines to out and returns when:
//
//   - follow=false: EOF is reached
//   - follow=true:  ctx is cancelled (e.g. SIGINT)
func runAgentsContext(ctx context.Context, out io.Writer, path string, mode contextMode, follow bool) error {
	if !follow {
		f, err := os.Open(path) //nolint:gosec // operator-supplied agent ref → daemon-supplied session log path; not a file-inclusion vector
		if err != nil {
			return fmt.Errorf("open session log %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		return renderEvents(out, sessionlog.Stream(f), mode)
	}
	// Tail mode: nxadm/tail's reader blocks until new data lands or
	// the handle is stopped. Ctx-cancel maps to t.Stop() so we
	// surface SIGINT cleanly without leaking the goroutine.
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
					// nxadm/tail surfaces inotify/rename errors here.
					// Best-effort: log via stderr and stop the loop.
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

// renderEvents pumps events from the channel into the writer using
// the selected view mode. Returns nil on clean channel close.
func renderEvents(out io.Writer, ch <-chan sessionlog.Event, mode contextMode) error {
	var tracker *usageTracker
	if mode == contextModeUsage {
		tracker = &usageTracker{started: time.Now()}
	}
	for ev := range ch {
		if err := renderOne(out, ev, mode, tracker); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		}
	}
	return nil
}

// renderOne writes one event in the active mode. Most modes share a
// "skip metadata RawEvents" rule — only raw mode prints them.
func renderOne(out io.Writer, ev sessionlog.Event, mode contextMode, usage *usageTracker) error {
	switch mode {
	case contextModeRaw:
		return writeLineRaw(out, ev)
	case contextModeConversation:
		return writeLineConversation(out, ev)
	case contextModeTools:
		return writeLineTools(out, ev)
	case contextModeThinking:
		return writeLineThinking(out, ev)
	case contextModeUsage:
		return writeLineUsage(out, ev, usage)
	case contextModeTree:
		return writeLineTree(out, ev)
	default:
		return writeLineRaw(out, ev)
	}
}

// writeLineRaw prints the verbatim JSONL bytes (followed by a
// newline). Parse-error RawEvents still pass through so the operator
// sees the broken line.
func writeLineRaw(out io.Writer, ev sessionlog.Event) error {
	raw := ev.RawLine()
	if len(raw) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(out, "%s\n", raw)
	return err
}

// writeLineConversation prints role-styled assistant/user text + tool
// blocks. Metadata RawEvents are dropped; tool_use and tool_result
// are summarised one-line each.
func writeLineConversation(out io.Writer, ev sessionlog.Event) error {
	switch m := ev.(type) {
	case sessionlog.UserMessage:
		if m.Text != "" {
			_, err := fmt.Fprintf(out, "user: %s\n", m.Text)
			return err
		}
		for _, b := range m.ContentBlocks {
			if tr, ok := b.(sessionlog.ToolResultBlock); ok {
				marker := "ok"
				if tr.IsError {
					marker = "ERR"
				}
				body := tr.Text
				if body == "" && len(tr.Blocks) > 0 {
					body = fmt.Sprintf("(%d sub-block(s))", len(tr.Blocks))
				}
				if _, err := fmt.Fprintf(out, "tool_result[%s] %s: %s\n",
					marker, tr.ToolUseID, oneLine(body)); err != nil {
					return err
				}
			}
		}
	case sessionlog.AssistantMessage:
		for _, b := range m.ContentBlocks {
			switch bb := b.(type) {
			case sessionlog.TextBlock:
				if _, err := fmt.Fprintf(out, "assistant: %s\n", bb.Text); err != nil {
					return err
				}
			case sessionlog.ToolUseBlock:
				if _, err := fmt.Fprintf(out, "tool_use[%s] %s %s\n",
					bb.ID, bb.Name, oneLine(string(bb.Input))); err != nil {
					return err
				}
			}
		}
	case sessionlog.SystemMessage:
		if _, err := fmt.Fprintf(out, "system[%s]\n", m.Subtype); err != nil {
			return err
		}
	}
	return nil
}

// writeLineTools prints only the tool-call timeline: every tool_use
// from assistant messages, every tool_result from user messages.
func writeLineTools(out io.Writer, ev sessionlog.Event) error {
	switch m := ev.(type) {
	case sessionlog.AssistantMessage:
		for _, b := range m.ContentBlocks {
			if tu, ok := b.(sessionlog.ToolUseBlock); ok {
				if _, err := fmt.Fprintf(out, "call %s %s %s\n",
					tu.ID, tu.Name, oneLine(string(tu.Input))); err != nil {
					return err
				}
			}
		}
	case sessionlog.UserMessage:
		for _, b := range m.ContentBlocks {
			if tr, ok := b.(sessionlog.ToolResultBlock); ok {
				marker := "ok"
				if tr.IsError {
					marker = "ERR"
				}
				body := tr.Text
				if body == "" && len(tr.Blocks) > 0 {
					body = fmt.Sprintf("(%d sub-block(s))", len(tr.Blocks))
				}
				if _, err := fmt.Fprintf(out, "result[%s] %s: %s\n",
					marker, tr.ToolUseID, oneLine(body)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeLineThinking prints only the assistant thinking content
// blocks. One line per block, prefixed with the assistant message's
// UUID for cross-reference.
func writeLineThinking(out io.Writer, ev sessionlog.Event) error {
	m, ok := ev.(sessionlog.AssistantMessage)
	if !ok {
		return nil
	}
	for _, b := range m.ContentBlocks {
		if th, ok := b.(sessionlog.ThinkingBlock); ok {
			if _, err := fmt.Fprintf(out, "thinking[%s]: %s\n",
				m.UUID, oneLine(th.Thinking)); err != nil {
				return err
			}
		}
	}
	return nil
}

// usageTracker accumulates per-turn token totals across assistant
// messages so the usage mode can print a running summary.
type usageTracker struct {
	started      time.Time
	turn         int
	totalInput   int
	totalOutput  int
	totalCreate  int
	totalRead    int
	totalCreate5 int
	totalCreate1 int
}

// writeLineUsage prints one line per assistant turn with the per-turn
// + running totals. Non-assistant events are skipped.
func writeLineUsage(out io.Writer, ev sessionlog.Event, t *usageTracker) error {
	if t == nil {
		// Defensive — callers in usage mode always supply a tracker, but
		// nilaway can't prove the cross-mode invariant in renderEvents.
		return nil
	}
	m, ok := ev.(sessionlog.AssistantMessage)
	if !ok {
		return nil
	}
	u := m.Usage
	// Skip records that didn't carry usage (mid-stream chunks).
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 {
		return nil
	}
	t.turn++
	t.totalInput += u.InputTokens
	t.totalOutput += u.OutputTokens
	t.totalCreate += u.CacheCreationInputTokens
	t.totalRead += u.CacheReadInputTokens
	t.totalCreate5 += u.CacheCreation.Ephemeral5mInputTokens
	t.totalCreate1 += u.CacheCreation.Ephemeral1hInputTokens
	ratio := 0.0
	denom := t.totalCreate + t.totalRead
	if denom > 0 {
		ratio = float64(t.totalRead) / float64(denom)
	}
	_, err := fmt.Fprintf(out,
		"turn=%d in=%d out=%d cache_create=%d (5m=%d 1h=%d) cache_read=%d hit=%.2f model=%s stop=%s\n",
		t.turn, u.InputTokens, u.OutputTokens,
		u.CacheCreationInputTokens,
		u.CacheCreation.Ephemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens,
		u.CacheReadInputTokens, ratio,
		m.Model, m.StopReason,
	)
	return err
}

// writeLineTree prints one line per assistant/user message annotated
// with isSidechain + parentUuid so the operator can grep for subagent
// dispatches. Phase A is intentionally flat — a hierarchical render
// lands with the TUI in the -i follow-up.
func writeLineTree(out io.Writer, ev sessionlog.Event) error {
	c := ev.Common()
	if c.UUID == "" {
		return nil
	}
	marker := "main"
	if c.IsSidechain {
		marker = "sidechain"
	}
	_, err := fmt.Fprintf(out, "[%s] %s parent=%s kind=%s\n",
		marker, c.UUID, c.ParentUUID, ev.Kind())
	return err
}

// oneLine collapses whitespace runs into single spaces and truncates
// to keep the per-line render predictable. Tools/conversation modes
// print one line per event, so a 200 KiB tool_result must not blow
// out the terminal.
func oneLine(s string) string {
	const cap = 240
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Fold any newline / tab sequences into single spaces.
	out := make([]byte, 0, len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			if !prevSpace {
				out = append(out, ' ')
				prevSpace = true
			}
			continue
		}
		out = append(out, string(r)...)
		prevSpace = r == ' '
	}
	folded := string(out)
	if len(folded) > cap {
		folded = folded[:cap] + "…"
	}
	return folded
}

// runAgentsContextWithTimeout is a thin wrapper used by tests to put
// a deadline on the run; the production verb relies on the cobra
// command's ctx (SIGINT handler) instead.
//
//nolint:unused // exercised via tests only
func runAgentsContextWithTimeout(parent context.Context, out io.Writer, path string, mode contextMode, follow bool, d time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	return runAgentsContext(ctx, out, path, mode, follow)
}

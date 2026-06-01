// agents_context.go owns `sextant agents context <agent>` — the
// operator surface for inspecting an agent's Claude Code SDK session.
//
// As of S0 (RFC §5.10) the persistent claude-projects bind-mount is gone.
// The verb now has two sources:
//
//   - LIVE (default + --follow): the NATS frame stream
//     (agents.<uuid>.frames). No mount, no file. --follow tails; without
//     it the verb replays every buffered frame then exits.
//
//   - AUTHORITATIVE (--raw / --backup): the in-container session .jsonl,
//     still ground-truth (never reconstructed from frames). --raw reads
//     it on demand via the read_file RPC; --backup reads the durable
//     host snapshot the reconciler wrote when the agent left running.
//
//   - --mode=<raw|conversation|tools|thinking|usage|tree> filters the
//     printed stream; raw is the floor.
//
//   - -i / --tui opens the interactive viewport over the authoritative
//     .jsonl (pkg/tui/contextview).
//
// The per-line rendering + mode vocabulary live in pkg/sessionlog
// (RenderLine / Mode / ParseMode) so the file-backed dump and the `-i`
// TUI render identically; the frame-stream view renders frames directly.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
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
	var raw bool
	var backup bool
	var modeFlag string
	cmd := &cobra.Command{
		Use:   "context <agent>",
		Short: "View the agent's Claude Code SDK session (live frames or authoritative JSONL)",
		Long: `View the agent's Claude Code SDK session.

There are two sources. By default the verb streams the agent's NATS
frame stream — the live view, no container mount required. --follow
keeps tailing; without it the verb replays the buffered frames and
exits.

--raw / --backup switch to the AUTHORITATIVE session .jsonl, the
rawest practical view (every assistant message, every tool call, the
full token-usage breakdown per turn). The .jsonl is never
reconstructed from frames:

  --raw     read the live in-container .jsonl on demand (read_file RPC)
  --backup  read the durable host snapshot taken when the agent last
            left running (works after the container is gone)

--follow turns the live view into a tail -f. -i / --tui opens the
interactive viewport over the authoritative .jsonl with mode keys 1-6.

--mode=<raw|conversation|tools|thinking|usage|tree> filters the
stream:

  raw           verbatim records (default; pipeable to jq)
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
			if raw && backup {
				return errUserUsage("--raw and --backup are mutually exclusive (--raw reads the live in-container .jsonl, --backup reads the host snapshot)")
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

			// Authoritative-source paths (-i, --raw, --backup) need the
			// session-log locators the daemon resolves from the agent record.
			if interactive || raw || backup {
				var resp sextantproto.GetAgentStatusResponse
				if err := cli.RPC(ctx, rpc.VerbGetAgentStatus,
					sextantproto.GetAgentStatusRequest{AgentID: id}, &resp); err != nil {
					return fmt.Errorf("get_agent_status: %w", err)
				}
				sl := resp.Status.SessionLog
				if sl == nil {
					return fmt.Errorf("agent %s has no session log info on the daemon (daemon may predate the S0 session-record change, or has no agents data root configured)", id)
				}
				if backup {
					return dumpSnapshot(cmd.OutOrStdout(), sl.SnapshotPath, id, mode)
				}
				if interactive {
					_ = cli.Close()
					return activeTUILauncher.RunAgentsContext(ctx, globalFlags.configDir, args[0], sl.ContainerJSONLPath, sl.SessionID)
				}
				// --raw: read the live in-container .jsonl on demand.
				return dumpRawJSONL(ctx, cmd.OutOrStdout(), cli, id, sl, mode)
			}

			// Default + --follow: the live frame stream (no mount, no file).
			return runFramesContext(ctx, cmd.OutOrStdout(), cli, id, mode, follow)
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false, "tail the live frame stream until interrupted")
	cmd.Flags().BoolVar(&raw, "raw", false, "read the authoritative in-container session .jsonl on demand (read_file)")
	cmd.Flags().BoolVar(&backup, "backup", false, "read the durable host snapshot of the session .jsonl (works after the container is gone)")
	cmd.Flags().BoolVarP(&interactive, "tui", "i", false, "open the interactive viewport over the authoritative .jsonl (mode keys 1-6)")
	cmd.Flags().StringVar(&modeFlag, "mode", string(sessionlog.ModeRaw),
		"view mode: raw|conversation|tools|thinking|usage|tree")
	return cmd
}

// runFramesContext is the LIVE view: subscribe to the agent's NATS frame
// stream and render each frame. follow=false replays every buffered frame
// then returns when the stream is drained (a deadline-bounded quiet
// period); follow=true tails until ctx cancellation. The reads bypass the
// F0 front-door RPC gauntlet — a frame subscribe is allowed for the
// operator principal (RFC §5.10).
func runFramesContext(ctx context.Context, out io.Writer, cli *client.Client, id uuid.UUID, mode sessionlog.Mode, follow bool) error {
	subject := "agents." + id.String() + ".frames"
	// DeliverAll so the buffered transcript replays from the start; the
	// subscription then continues live (relevant only under --follow).
	frames, err := cli.Subscribe(ctx, subject, client.WithDeliverAll())
	if err != nil {
		return fmt.Errorf("subscribe frames: %w", err)
	}

	var acc *sessionlog.UsageAccumulator
	if mode == sessionlog.ModeUsage {
		acc = &sessionlog.UsageAccumulator{}
	}

	// In one-shot (non-follow) mode we exit once the buffered frames are
	// drained. JetStream gives no "caught up" signal on a push channel, so
	// we treat an idleTimeout with no new frame as "drained."
	const idleTimeout = 750 * time.Millisecond
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	if follow {
		idle.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-idle.C:
			if !follow {
				return nil
			}
		case msg, ok := <-frames:
			if !ok {
				return nil
			}
			if msg.Err != nil {
				printf(out, "[decode error seq=%d]: %v\n", msg.StreamSeq, msg.Err)
				_ = msg.Ack()
				if !follow {
					idle.Reset(idleTimeout)
				}
				continue
			}
			line := renderFrameLine(msg.Envelope, mode, acc)
			if line != "" {
				if _, werr := fmt.Fprintln(out, line); werr != nil {
					if errors.Is(werr, io.ErrClosedPipe) {
						return nil
					}
					return werr
				}
			}
			_ = msg.Ack()
			if !follow {
				idle.Reset(idleTimeout)
			}
		}
	}
}

// dumpRawJSONL reads the live in-container session .jsonl on demand via
// the read_file RPC and renders it through the chosen mode. When no live
// container is serving the read, it falls back to the durable host
// snapshot so `--raw` still produces the authoritative transcript for a
// stopped agent.
func dumpRawJSONL(ctx context.Context, out io.Writer, cli *client.Client, id uuid.UUID, sl *sextantproto.SessionLogInfo, mode sessionlog.Mode) error {
	if sl.SessionID == "" || sl.ContainerJSONLPath == "" {
		return fmt.Errorf("agent %s has not recorded a session yet (no SDK turn has completed; prompt the agent then retry)", id)
	}
	var resp sextantproto.ReadFileResponse
	rerr := cli.RPC(ctx, rpc.VerbReadFile,
		sextantproto.ReadFileRequest{AgentID: id, Path: sl.ContainerJSONLPath}, &resp)
	if rerr != nil {
		// No live incarnation (or the file isn't readable) — fall back to the
		// durable snapshot the reconciler took on stop, if one exists.
		if sl.SnapshotPath != "" {
			if _, statErr := os.Stat(sl.SnapshotPath); statErr == nil {
				fmt.Fprintf(os.Stderr, "agents context: live read unavailable (%v); falling back to the on-stop snapshot\n", rerr)
				return dumpSnapshot(out, sl.SnapshotPath, id, mode)
			}
		}
		return fmt.Errorf("read_file %s: %w", sl.ContainerJSONLPath, rerr)
	}
	return renderEvents(out, sessionlog.Stream(bytes.NewReader(resp.Content)), mode)
}

// dumpSnapshot renders the durable host snapshot of the session .jsonl —
// the post-stop backup the reconciler wrote into the agent data dir. The
// snapshot is on the daemon host's filesystem (single-host today), which
// the operator CLI shares, so it reads directly off disk.
func dumpSnapshot(out io.Writer, snapshotPath string, id uuid.UUID, mode sessionlog.Mode) error {
	if snapshotPath == "" {
		return fmt.Errorf("agent %s has no session snapshot path (the daemon has no agents data root configured)", id)
	}
	f, err := os.Open(snapshotPath) //nolint:gosec // daemon-supplied snapshot path under the agents data root
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agent %s has no session snapshot yet (the reconciler writes one when the agent leaves running; it has not stopped, or never completed a turn)", id)
		}
		return fmt.Errorf("open snapshot %s: %w", snapshotPath, err)
	}
	defer func() { _ = f.Close() }()
	return renderEvents(out, sessionlog.Stream(f), mode)
}

// renderFrameLine renders one frame envelope under the chosen mode. The
// frame stream is the live view; full-fidelity modes (usage/tree, deep
// thinking) are most precise off the authoritative .jsonl (--raw), but the
// common modes render directly off frames here.
func renderFrameLine(env sextantproto.Envelope, mode sessionlog.Mode, acc *sessionlog.UsageAccumulator) string {
	if mode == sessionlog.ModeRaw {
		raw, err := json.Marshal(env)
		if err != nil {
			return ""
		}
		return string(raw)
	}
	var fp sextantproto.AgentFramePayload
	if err := json.Unmarshal(env.Payload, &fp); err != nil {
		return ""
	}
	ts := env.Ts.Format(time.RFC3339)
	switch mode {
	case sessionlog.ModeConversation:
		switch fp.FrameKind {
		case sextantproto.FrameAssistantText:
			return fmt.Sprintf("%s [assistant] %s", ts, frameText(fp))
		case sextantproto.FrameToolCall:
			return fmt.Sprintf("%s [tool_call] %s %s", ts, fp.ToolName, summarizeBody(fp.Body))
		case sextantproto.FrameToolResult:
			return fmt.Sprintf("%s [tool_result] %s %s", ts, fp.ToolName, summarizeBody(fp.Body))
		case sextantproto.FrameSystemNote:
			return fmt.Sprintf("%s [system] %s", ts, summarizeBody(fp.Body))
		case sextantproto.FrameError:
			return fmt.Sprintf("%s [error] %s", ts, summarizeBody(fp.Body))
		}
		return ""
	case sessionlog.ModeTools:
		switch fp.FrameKind {
		case sextantproto.FrameToolCall:
			return fmt.Sprintf("%s [tool_call] %s %s", ts, fp.ToolName, summarizeBody(fp.Body))
		case sextantproto.FrameToolResult:
			return fmt.Sprintf("%s [tool_result] %s %s", ts, fp.ToolName, summarizeBody(fp.Body))
		}
		return ""
	case sessionlog.ModeThinking:
		// Frames don't carry a distinct thinking block today; thinking is
		// most precise off the authoritative .jsonl (--raw --mode=thinking).
		return ""
	case sessionlog.ModeUsage:
		if fp.Tokens == nil {
			return ""
		}
		if acc != nil {
			// Mirror the .jsonl usage accumulator's per-turn shape as best the
			// frame tokens allow.
			return fmt.Sprintf("%s [usage] in=%d out=%d cache_read=%d cache_created=%d",
				ts, fp.Tokens.Input, fp.Tokens.Output, fp.Tokens.CacheRead, fp.Tokens.CacheCreated)
		}
		return ""
	case sessionlog.ModeTree:
		// The subagent tree is a JSONL-structural view (parentUuid /
		// isSidechain) the frame stream doesn't carry; use --raw --mode=tree.
		return ""
	}
	return ""
}

// frameText extracts assistant text from a frame body (text or content).
func frameText(fp sextantproto.AgentFramePayload) string {
	if t, ok := fp.Body["text"].(string); ok && t != "" {
		return t
	}
	if c, ok := fp.Body["content"].(string); ok {
		return c
	}
	return ""
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

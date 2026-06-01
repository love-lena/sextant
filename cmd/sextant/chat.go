// chat.go owns `sextant agents chat <agent> [text]`. This single verb
// folds the previous `sextant ask` (one-shot send + wait) and
// `sextant conversation` (interactive TUI) per
// `slug:feat-cli-resource-verb-cleanup`.
//
// Mode selection at runtime:
//
//   - `sextant agents chat <agent>`            → TUI (default; 90% case).
//   - `sextant agents chat <agent> "<text>"`   → one-shot (send + print + exit).
//   - `echo … | sextant agents chat <agent>`  → one-shot, prompt read from stdin.
//   - `sextant agents chat <agent> --json …`  → NDJSON envelope output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/chat"
)

// errAskTimeout is the sentinel returned from streamAskTurn when the
// per-turn deadline elapses without a terminating lifecycle envelope.
var errAskTimeout = errors.New("ask: timeout waiting for turn_ended lifecycle")

// newAgentsChatCmd wires `sextant agents chat <agent> [text]`. Mode is
// determined by args / stdin / --json — see file header for the rule.
func newAgentsChatCmd() *cobra.Command {
	var (
		timeout time.Duration
		tail    bool
		fromSeq uint64
		read    bool
	)
	cmd := &cobra.Command{
		Use:   "chat <agent> [text]",
		Short: "Open the chat TUI for an agent (or one-shot prompt+wait)",
		Long: `With no positional text and a TTY stdin, opens the modal chat TUI.
With a positional text argument OR piped stdin, sends one prompt, waits
for the next lifecycle.turn_ended, prints the agent's reply, and exits.

--json swaps the renderer for raw envelope NDJSON. --tail (TUI mode)
closes the window on lifecycle.ended. --read opens the TUI without a
composer (read-only).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Determine mode. One-shot if a text arg was given OR if stdin
			// is piped (not a TTY).
			oneShotText := ""
			oneShot := false
			if len(args) == 2 {
				oneShot = true
				oneShotText = args[1]
			} else if !stdinIsTTY() {
				body, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				oneShotText = strings.TrimRight(string(body), "\n")
				oneShot = oneShotText != ""
			}

			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			id, err := resolveAgentRef(ctx, cli, args[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("agent: %v", err))
			}

			if oneShot {
				return doOneShot(ctx, cmd.OutOrStdout(), cli, id, oneShotText, timeout)
			}
			return doChatTUI(ctx, cmd.OutOrStdout(), cli, id, read, tail, fromSeq)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second,
		"one-shot mode: hard cap on turn duration")
	cmd.Flags().BoolVar(&tail, "tail", false,
		"TUI mode: exit on the next lifecycle.ended for this agent")
	cmd.Flags().Uint64Var(&fromSeq, "from-seq", 0,
		"TUI mode: resume from JetStream stream sequence N")
	cmd.Flags().BoolVar(&read, "read", false,
		"TUI mode: open the chat TUI without a composer (read-only)")
	return cmd
}

// stdinIsTTY reports whether stdin is a terminal. Indirected via
// isatty so tests can stub it if needed (currently not stubbed).
func stdinIsTTY() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}

// doOneShot implements the legacy `sextant ask` flow: subscribe BEFORE
// publishing, publish the prompt, stream frames + lifecycle until the
// next turn_ended/ended, exit.
func doOneShot(ctx context.Context, w io.Writer, cli *client.Client, id uuid.UUID, text string, timeout time.Duration) error {
	if timeout <= 0 {
		return errUserUsage("--timeout must be positive")
	}
	framesCh, err := cli.Subscribe(ctx, "agents."+id.String()+".frames")
	if err != nil {
		return fmt.Errorf("subscribe frames: %w", err)
	}
	lifecycleCh, err := cli.Subscribe(ctx, "agents."+id.String()+".lifecycle")
	if err != nil {
		return fmt.Errorf("subscribe lifecycle: %w", err)
	}
	req := sextantproto.PromptAgentRequest{AgentID: id, Content: text}
	var resp sextantproto.PromptAgentResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbPromptAgent, req, &resp); err != nil {
		return fmt.Errorf("prompt_agent: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("prompt_agent: daemon returned ok=false")
	}
	return streamAskTurn(ctx, w, framesCh, lifecycleCh, id, globalFlags.asJSON, timeout)
}

// streamAskTurn is the testable core of one-shot chat. Reuses the same
// renderFrame / renderLifecycle helpers as the TUI fallback path.
//
// On timeout, returns an error enriched with the last-known lifecycle
// state so the operator gets a remedy ("agent ended — restart with …")
// instead of a bare timeout. The lifecycle channel feeds both the
// terminal-state detection and this diagnostic — every observed
// transition updates `lastLifecycle`. Per
// feat-ask-conversation-self-diagnose-on-timeout.
func streamAskTurn(
	ctx context.Context,
	w io.Writer,
	frames <-chan client.Message,
	lifecycle <-chan client.Message,
	agentID uuid.UUID,
	asJSON bool,
	timeout time.Duration,
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var lastLifecycle sextantproto.LifecycleEvent

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return askTimeoutError(timeout, lastLifecycle, agentID)
		case msg, ok := <-frames:
			if !ok {
				return fmt.Errorf("%w: frames channel closed before turn_ended", errAskTimeout)
			}
			if msg.Err != nil {
				printf(w, "[decode error seq=%d]: %v\n", msg.StreamSeq, msg.Err)
				continue
			}
			if err := renderFrame(w, msg, asJSON); err != nil {
				return err
			}
			_ = msg.Ack()
		case msg, ok := <-lifecycle:
			if !ok {
				return fmt.Errorf("%w: lifecycle channel closed before turn_ended", errAskTimeout)
			}
			if msg.Err != nil {
				continue
			}
			if msg.Envelope.Kind != sextantproto.KindLifecycle {
				continue
			}
			var p sextantproto.LifecyclePayload
			if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
				continue
			}
			if p.AgentUUID != agentID {
				_ = msg.Ack()
				continue
			}
			lastLifecycle = p.Transition
			terminal := p.Transition == sextantproto.LifecycleTurnEnded ||
				p.Transition == sextantproto.LifecycleEnded
			if terminal {
				drainAskFrames(w, frames, asJSON)
			}
			if err := renderLifecycle(w, msg, p, asJSON); err != nil {
				return err
			}
			_ = msg.Ack()
			if terminal {
				return nil
			}
		}
	}
}

// askTimeoutError builds the timeout error body. When the lifecycle
// stream surfaced a terminal transition before the deadline, the
// message names the state and the remedy. When the stream was silent
// (the common "agent unresponsive" case), the message mentions that
// the prompt was accepted but no frames arrived.
func askTimeoutError(timeout time.Duration, last sextantproto.LifecycleEvent, agentID uuid.UUID) error {
	switch last {
	case sextantproto.LifecycleEnded:
		return fmt.Errorf("%w: agent lifecycle=ended; restart with `sextant agents restart %s`",
			errAskTimeout, agentID)
	case sextantproto.LifecycleCrashedEvent:
		return fmt.Errorf("%w: agent lifecycle=crashed; restart with `sextant agents restart %s`",
			errAskTimeout, agentID)
	case sextantproto.LifecyclePausedEvent:
		// No `sextant agents resume` verb today — restart is the only
		// real recovery. See [[feat-agents-resume-verb]] follow-up.
		return fmt.Errorf("%w: agent lifecycle=paused; restart with `sextant agents restart %s`",
			errAskTimeout, agentID)
	case sextantproto.LifecycleArchivedEvent:
		return fmt.Errorf("%w: agent lifecycle=archived; spawn a new agent instead",
			errAskTimeout)
	case "":
		return fmt.Errorf("%w (waited %s; no lifecycle activity — try `sextant logs --tail 50` or extend --timeout)",
			errAskTimeout, timeout)
	default:
		// Saw started / resumed / restarted / turn_ended (mid-turn) but no terminal.
		return fmt.Errorf("%w (waited %s; agent is alive but didn't complete a turn — extend --timeout or check `sextant logs --tail 50`)",
			errAskTimeout, timeout)
	}
}

// drainAskFrames consumes everything already buffered in the frames
// channel without blocking. Used at turn-end so a final assistant frame
// that arrived in the same scheduler tick as turn_ended still renders
// before we exit.
func drainAskFrames(w io.Writer, frames <-chan client.Message, asJSON bool) {
	for {
		select {
		case msg, ok := <-frames:
			if !ok {
				return
			}
			if msg.Err != nil {
				printf(w, "[decode error seq=%d]: %v\n", msg.StreamSeq, msg.Err)
				continue
			}
			if err := renderFrame(w, msg, asJSON); err != nil {
				return
			}
			_ = msg.Ack()
		default:
			return
		}
	}
}

// doChatTUI is the legacy `sextant conversation` flow: subscribe to
// frames + lifecycle, hand the channels to the chat TUI (or to the
// NDJSON streamer when --json is set).
func doChatTUI(ctx context.Context, w io.Writer, cli *client.Client, id uuid.UUID, read, tail bool, fromSeq uint64) error {
	subject := "agents." + id.String() + ".frames"
	frameOpts := []client.SubscribeOption{}
	if fromSeq > 0 {
		frameOpts = append(frameOpts, client.WithStartSeq(fromSeq))
	}
	frames, err := cli.Subscribe(ctx, subject, frameOpts...)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	lifecycle, err := cli.Subscribe(ctx, "agents."+id.String()+".lifecycle")
	if err != nil {
		return fmt.Errorf("subscribe lifecycle: %w", err)
	}
	return runConversationDispatch(ctx, w, cli, frames, lifecycle, id, read, globalFlags.asJSON, tail)
}

// streamConversation is the testable core: it consumes the frames
// channel and (optionally) the lifecycle channel, writing rendered
// output to w. Exits when ctx is canceled, when frames closes, or
// when tail==true and a lifecycle.ended for agentID arrives.
func streamConversation(
	ctx context.Context,
	w io.Writer,
	frames <-chan client.Message,
	lifecycle <-chan client.Message,
	agentID uuid.UUID,
	asJSON, tailUntilEnd bool,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-frames:
			if !ok {
				return nil
			}
			if msg.Err != nil {
				printf(w, "[decode error seq=%d]: %v\n", msg.StreamSeq, msg.Err)
				continue
			}
			if err := renderFrame(w, msg, asJSON); err != nil {
				return err
			}
			_ = msg.Ack()
		case msg, ok := <-lifecycle:
			if !ok {
				if tailUntilEnd {
					return nil
				}
				continue
			}
			if msg.Err != nil {
				continue
			}
			if msg.Envelope.Kind != sextantproto.KindLifecycle {
				continue
			}
			var p sextantproto.LifecyclePayload
			if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
				continue
			}
			if p.AgentUUID != agentID {
				_ = msg.Ack()
				continue
			}
			if err := renderLifecycle(w, msg, p, asJSON); err != nil {
				return err
			}
			_ = msg.Ack()
			if tailUntilEnd && p.Transition == sextantproto.LifecycleEnded {
				return nil
			}
		}
	}
}

// renderFrame writes one frame to w. JSON mode emits one envelope
// per line (NDJSON); text mode emits a compact human-readable line.
func renderFrame(w io.Writer, msg client.Message, asJSON bool) error {
	if asJSON {
		raw, err := json.Marshal(msg.Envelope)
		if err != nil {
			return fmt.Errorf("marshal envelope: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	var fp sextantproto.AgentFramePayload
	if err := json.Unmarshal(msg.Envelope.Payload, &fp); err != nil {
		preview := string(msg.Envelope.Payload)
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		printf(w, "%s [%s] %s\n", msg.Envelope.Ts.Format(time.RFC3339), msg.Envelope.Kind, preview)
		return nil
	}
	ts := msg.Envelope.Ts.Format(time.RFC3339)
	switch fp.FrameKind {
	case sextantproto.FrameAssistantText:
		text, _ := fp.Body["text"].(string)
		if text == "" {
			text, _ = fp.Body["content"].(string)
		}
		printf(w, "%s [assistant] %s\n", ts, text)
	case sextantproto.FrameToolCall:
		printf(w, "%s [tool_call] %s %s\n", ts, fp.ToolName, summarizeBody(fp.Body))
	case sextantproto.FrameToolResult:
		printf(w, "%s [tool_result] %s %s\n", ts, fp.ToolName, summarizeBody(fp.Body))
	case sextantproto.FrameSystemNote:
		printf(w, "%s [system] %s\n", ts, summarizeBody(fp.Body))
	case sextantproto.FrameError:
		printf(w, "%s [error] %s\n", ts, summarizeBody(fp.Body))
	default:
		printf(w, "%s [%s] %s\n", ts, fp.FrameKind, summarizeBody(fp.Body))
	}
	return nil
}

// renderLifecycle writes one lifecycle envelope to w.
func renderLifecycle(w io.Writer, msg client.Message, p sextantproto.LifecyclePayload, asJSON bool) error {
	if asJSON {
		raw, err := json.Marshal(msg.Envelope)
		if err != nil {
			return fmt.Errorf("marshal lifecycle envelope: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	ts := msg.Envelope.Ts.Format(time.RFC3339)
	if p.Reason != "" {
		printf(w, "%s [lifecycle] transition=%s reason=%q\n", ts, p.Transition, p.Reason)
	} else {
		printf(w, "%s [lifecycle] transition=%s\n", ts, p.Transition)
	}
	return nil
}

// summarizeBody flattens a body map into "k=v k=v" pairs so the text
// renderer stays single-line.
func summarizeBody(body map[string]any) string {
	if len(body) == 0 {
		return ""
	}
	parts := make([]string, 0, len(body))
	for k, v := range body {
		switch vv := v.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%s=%q", k, truncate(vv, 60)))
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			_ = vv
		}
	}
	return strings.Join(parts, " ")
}

// chatRunnerIface lets tests substitute a fake for the heavy
// bubbletea-bound runner.
type chatRunnerIface interface {
	Run(
		ctx context.Context,
		w io.Writer,
		cli *client.Client,
		frames <-chan client.Message,
		lifecycle <-chan client.Message,
		id uuid.UUID,
		read, asJSON, tail bool,
	) error
}

type chatRunnerFunc func(
	context.Context, io.Writer, *client.Client,
	<-chan client.Message, <-chan client.Message,
	uuid.UUID, bool, bool, bool,
) error

func (f chatRunnerFunc) Run(
	ctx context.Context, w io.Writer, cli *client.Client,
	frames <-chan client.Message, lifecycle <-chan client.Message,
	id uuid.UUID, read, asJSON, tail bool,
) error {
	return f(ctx, w, cli, frames, lifecycle, id, read, asJSON, tail)
}

// chatRunner is the swappable seam. Tests overwrite it.
var chatRunner chatRunnerIface = chatRunnerFunc(realChatRun)

// realChatRun is the production dispatch into pkg/tui/chat.Run.
func realChatRun(
	ctx context.Context, _ io.Writer, cli *client.Client,
	frames <-chan client.Message, lifecycle <-chan client.Message,
	id uuid.UUID, read, _ bool, tail bool,
) error {
	agentName := resolveAgentName(ctx, cli, id)
	return chat.Run(chat.RunConfig{
		Ctx:          ctx,
		Bus:          chat.NewClientBus(cli),
		AgentID:      id,
		AgentName:    agentName,
		Read:         read,
		TailUntilEnd: tail,
		Frames:       frames,
		Lifecycle:    lifecycle,
	})
}

// resolveAgentName best-effort fetches the friendly name for an agent
// UUID via list_agents. Falls back to the UUID string on failure.
func resolveAgentName(ctx context.Context, cli *client.Client, id uuid.UUID) string {
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var resp sextantproto.ListAgentsResponse
	if err := cli.RPC(lookup, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
		return id.String()
	}
	for _, a := range resp.Agents {
		if a.UUID == id {
			return a.Name
		}
	}
	return id.String()
}

// runConversationDispatch routes between the NDJSON streamer and the
// chat TUI.
func runConversationDispatch(
	ctx context.Context, w io.Writer, cli *client.Client,
	frames <-chan client.Message, lifecycle <-chan client.Message,
	id uuid.UUID, read, asJSON, tail bool,
) error {
	if asJSON {
		return streamConversation(ctx, w, frames, lifecycle, id, asJSON, tail)
	}
	return chatRunner.Run(ctx, w, cli, frames, lifecycle, id, read, asJSON, tail)
}

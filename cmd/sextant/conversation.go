package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

const conversationUsage = `usage: sextant conversation <agent_uuid> [--tail] [--from-seq N] [--json]

Stream agents.<uuid>.frames + agents.<uuid>.lifecycle in human-readable
form. Lifecycle transitions (started, turn_ended, ended, …) are rendered
inline so external tooling can tell when a turn finishes. Defaults to a
forever-live tail; --tail exits on the next lifecycle.ended event for
the same agent. --from-seq N resumes from the given JetStream stream
sequence so the operator can pick up after a disconnect.`

// runConversation — `sextant conversation <agent> [--tail] [--from-seq N]`.
func runConversation(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant conversation", flag.ContinueOnError)
	var tail bool
	var fromSeq uint64
	fs.BoolVar(&tail, "tail", false, "exit on the next lifecycle.ended for this agent")
	fs.Uint64Var(&fromSeq, "from-seq", 0, "resume from JetStream stream sequence N")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintln(os.Stderr, conversationUsage)
		return errUserUsage("sextant conversation <agent_uuid>")
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

	subject := "agents." + id.String() + ".frames"

	// Always subscribe to lifecycle so per-turn transitions (notably
	// turn_ended, emitted by the sidecar SDK driver at the end of every
	// turn) show up in the rendered conversation. Without this, external
	// tooling — and the operator at the terminal — has no way to tell
	// when a turn finished without polling for ancillary signals. The
	// `--tail` flag still controls whether streamConversation exits when
	// a lifecycle.ended for this agent arrives; turn_ended is rendered
	// in both tail and non-tail modes. See
	// plans/issues/bug-lifecycle-turn-ended-missing.md.
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

	return streamConversation(ctx, os.Stdout, frames, lifecycle, id, opts.asJSON, tail)
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
			// Only render transitions for the agent we're following. Lifecycle
			// envelopes carry agent_uuid in their payload; the subject already
			// scopes the subscription, but the payload check is cheap insurance
			// against accidental cross-agent rendering if the subject is ever
			// widened.
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
		// Frame doesn't decode as an AgentFramePayload — surface the kind
		// + payload preview so the operator at least sees something.
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

// renderLifecycle writes one lifecycle envelope to w. JSON mode emits
// the raw envelope (NDJSON, same shape as frame rendering); text mode
// emits a compact `[ts] [lifecycle] transition=<x>` line, plus any
// reason if the sidecar provided one. The sidecar publishes
// `transition=turn_ended` after every SDK turn (success or error);
// rendering it here is what lets external tooling — and the operator
// reading `sextant conversation` — see when a turn finishes.
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
// renderer stays single-line. Falls back to a compact JSON if the
// flatten produces nothing useful.
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
			_ = vv // keep the type switch exhaustive
		}
	}
	return strings.Join(parts, " ")
}

// truncate is shared with doctor.go (same package). The conversation
// renderer reuses it for body summarization.

// _ keep the errors import alive for future helpers (currently the
// streaming path doesn't directly use it).
var _ = errors.New

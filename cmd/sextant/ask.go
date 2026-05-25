package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

const askUsage = `usage: sextant ask <agent> "<text>" [--timeout 60s] [--json]

Synchronous one-shot: publish a prompt to <agent>, then stream the
agent's frames + lifecycle inline until the next lifecycle.turn_ended
(or .ended) arrives. Exits 0 on a clean turn finish, non-zero on
--timeout expiry.

<agent> accepts an agent name or a UUID, same as ` + "`sextant agents archive`" + `.

Use this for daily-drive assistant chats where the two-pane
` + "`sextant conversation ... & sextant agents prompt ...`" + ` workflow is
overkill.`

// errAskTimeout is the sentinel returned from streamAskTurn when the
// per-turn deadline elapses without a terminating lifecycle envelope.
// Exported (via errors.Is) so tests can assert the exact failure mode
// without string-matching.
var errAskTimeout = errors.New("ask: timeout waiting for turn_ended lifecycle")

// runAsk implements `sextant ask <agent> "<text>"`. The order of
// operations matters: subscribe to both frame and lifecycle subjects
// BEFORE publishing the prompt RPC. JetStream subscriptions created
// after the daemon has already published the first frame would miss
// that frame (we use a fresh ephemeral consumer with the default
// `start-time = now`).
func runAsk(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant ask", flag.ContinueOnError)
	var timeout time.Duration
	fs.DurationVar(&timeout, "timeout", 60*time.Second,
		"hard cap on turn duration; exits non-zero if no turn_ended within this window")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, askUsage)
		return errUserUsage(`sextant ask <agent> "<text>"`)
	}
	if timeout <= 0 {
		return errUserUsage("--timeout must be positive")
	}

	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	id, err := resolveAgentID(ctx, cli, rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("agent: %v", err))
	}

	// Subscribe BEFORE publishing. The ordering is intentional and
	// load-bearing: with `start-time = now` JetStream semantics, a
	// subscription created after the daemon has already routed the
	// prompt → frame chain can race past the first frame. By the time
	// the prompt RPC returns the daemon may have already pushed
	// frames; we need the consumer in place first.
	framesCh, err := cli.Subscribe(ctx, "agents."+id.String()+".frames")
	if err != nil {
		return fmt.Errorf("subscribe frames: %w", err)
	}
	lifecycleCh, err := cli.Subscribe(ctx, "agents."+id.String()+".lifecycle")
	if err != nil {
		return fmt.Errorf("subscribe lifecycle: %w", err)
	}

	// Publish the prompt. The PromptAgentResponse only confirms the
	// daemon accepted the request — the actual assistant reply arrives
	// out-of-band on the frames subject, which is what streamAskTurn
	// reads.
	req := sextantproto.PromptAgentRequest{
		AgentID: id,
		Content: rest[1],
	}
	var resp sextantproto.PromptAgentResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbPromptAgent, req, &resp); err != nil {
		return fmt.Errorf("prompt_agent: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("prompt_agent: daemon returned ok=false")
	}

	return streamAskTurn(ctx, os.Stdout, framesCh, lifecycleCh, id, opts.asJSON, timeout)
}

// streamAskTurn is the testable core of `sextant ask`. It reuses the
// same renderFrame / renderLifecycle helpers as `sextant conversation`
// so text and NDJSON modes are visually identical across the two verbs.
//
// Exits with nil error on the first lifecycle.turn_ended OR
// lifecycle.ended for agentID. Exits with errAskTimeout if the timeout
// elapses first. Returns ctx.Err() if the parent context is canceled
// (e.g. the operator hit Ctrl-C).
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

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w (waited %s)", errAskTimeout, timeout)
		case msg, ok := <-frames:
			if !ok {
				// frames channel closed before turn_ended — surface as
				// timeout-shaped error so the operator sees a clear
				// "the stream went away" signal rather than exit 0.
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
			// Only consider lifecycle for the agent we're addressing.
			// Subject already scopes this but the payload check stays
			// for defense-in-depth against accidental subject widening.
			if p.AgentUUID != agentID {
				_ = msg.Ack()
				continue
			}
			terminal := p.Transition == sextantproto.LifecycleTurnEnded ||
				p.Transition == sextantproto.LifecycleEnded
			// On a terminal transition, drain any frames that are already
			// queued before we render the lifecycle line — otherwise a
			// select-race between frames and lifecycle can land turn_ended
			// above the assistant text that completed the turn, which is
			// confusing for the operator. The drain is non-blocking; we
			// only consume what's already sitting in the channel.
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
				// Best-effort drain; a render failure on the drain path
				// is non-fatal — the terminal lifecycle is about to land.
				return
			}
			_ = msg.Ack()
		default:
			return
		}
	}
}

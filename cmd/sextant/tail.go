// tail.go owns `runEventsTail` — the body of `sextant events tail
// <subject>`. The cobra wiring lives in events.go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// runEventsTail wires the subscribe + stream. Called from the cobra
// RunE in events.go. If duration > 0, wraps ctx with WithTimeout so
// the subscription exits cleanly at the deadline (--for flag).
func runEventsTail(cmd *cobra.Command, subject string, fromSeq uint64, duration time.Duration) error {
	ctx := cmd.Context()
	if duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	}
	cli, _, err := connectAgent(ctx, globalFlags.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	subOpts := []client.SubscribeOption{}
	if fromSeq > 0 {
		subOpts = append(subOpts, client.WithStartSeq(fromSeq))
	}
	msgs, err := cli.Subscribe(ctx, subject, subOpts...)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	return streamTail(ctx, cmd.OutOrStdout(), msgs, globalFlags.asJSON)
}

// streamTail is the testable core: it reads envelopes off ch and writes
// rendered output to w until ctx is canceled or ch closes.
func streamTail(ctx context.Context, w io.Writer, ch <-chan client.Message, asJSON bool) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if msg.Err != nil {
				printf(os.Stderr, "[decode error subject=%s seq=%d]: %v\n", msg.Subject, msg.StreamSeq, msg.Err)
				continue
			}
			if err := renderTailEnvelope(w, msg, asJSON); err != nil {
				return err
			}
			_ = msg.Ack()
		}
	}
}

// renderTailEnvelope writes one envelope to w.
func renderTailEnvelope(w io.Writer, msg client.Message, asJSON bool) error {
	if asJSON {
		raw, err := json.Marshal(msg.Envelope)
		if err != nil {
			return fmt.Errorf("marshal envelope: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	ts := msg.Envelope.Ts.Format(time.RFC3339)
	summary := summarizeEnvelope(msg.Envelope)
	if summary == "" {
		printf(w, "[%s] %s  kind=%s\n", ts, msg.Subject, msg.Envelope.Kind)
	} else {
		printf(w, "[%s] %s  kind=%s  %s\n", ts, msg.Subject, msg.Envelope.Kind, summary)
	}
	return nil
}

// summarizeEnvelope produces a one-line summary tailored to the envelope's kind.
func summarizeEnvelope(env sextantproto.Envelope) string {
	switch env.Kind {
	case sextantproto.KindAgentFrame:
		var fp sextantproto.AgentFramePayload
		if err := json.Unmarshal(env.Payload, &fp); err != nil {
			return payloadPreview(env.Payload)
		}
		if fp.ToolName != "" {
			return fmt.Sprintf("frame=%s tool=%s", fp.FrameKind, fp.ToolName)
		}
		return fmt.Sprintf("frame=%s", fp.FrameKind)
	case sextantproto.KindLifecycle:
		var p sextantproto.LifecyclePayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return payloadPreview(env.Payload)
		}
		return fmt.Sprintf("agent=%s transition=%s state=%s", p.AgentUUID, p.Transition, p.State)
	case sextantproto.KindAudit:
		var p sextantproto.AuditPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return payloadPreview(env.Payload)
		}
		return fmt.Sprintf("actor=%s action=%s result=%s", p.Actor, p.Action, p.Result)
	case sextantproto.KindHeartbeat:
		return fmt.Sprintf("from=%s/%s", env.From.Kind, env.From.ID)
	default:
		return payloadPreview(env.Payload)
	}
}

// payloadPreview returns a short stringified preview of the payload.
func payloadPreview(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	s := string(raw)
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

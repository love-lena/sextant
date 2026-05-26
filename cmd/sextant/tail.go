package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

const tailUsage = `usage: sextant tail <subject> [--from-seq N] [--json]

Subscribe to an arbitrary NATS subject and print envelopes as they
arrive. The subject accepts NATS wildcards (` + "`*`" + ` matches one token,
` + "`>`" + ` matches one or more).

Examples:
  sextant tail 'agents.>'                    every agent's events
  sextant tail 'agents.*.lifecycle'          lifecycle across all agents
  sextant tail 'telemetry.>'                 OTel firehose
  sextant tail 'sextant.system.>'            daemon self-management events
  sextant tail 'audit.>' --from-seq 12345    gap-fill audit log from seq

Default output is one human-readable line per envelope:
  [ts] subject  kind=<kind>  <one-line summary>
--json swaps the renderer for raw envelope JSON, one per line (NDJSON).

Note: a JetStream consumer binds to exactly one stream, so the subject
must resolve to a single stream. A bare ` + "`>`" + ` firehose spans every
stream and cannot be subscribed to as one consumer — use a stream-scoped
prefix (e.g. ` + "`audit.>`" + `, ` + "`agents.>`" + `, ` + "`telemetry.>`" + `) instead.`

// runTail — `sextant tail <subject> [--from-seq N] [--json]`.
//
// Thin wrapper over pkg/client.Subscribe. Reuses the same auth + connect
// path every other operator verb uses; the only thing on top is a
// per-envelope renderer that prints either a one-line human summary or
// the raw envelope JSON.
func runTail(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant tail", flag.ContinueOnError)
	var fromSeq uint64
	fs.Uint64Var(&fromSeq, "from-seq", 0, "resume from JetStream stream sequence N")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintln(os.Stderr, tailUsage)
		return errUserUsage("sextant tail <subject>")
	}
	subject := rest[0]
	if subject == "" {
		return errUserUsage("subject must be non-empty")
	}

	cli, _, err := connectAgent(ctx, opts.configDir)
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
	return streamTail(ctx, os.Stdout, msgs, opts.asJSON)
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

// renderTailEnvelope writes one envelope to w. JSON mode emits raw
// envelope JSON, one per line (NDJSON). Text mode emits a single
// `[ts] subject  kind=<kind>  <summary>` line with a kind-specific
// summary.
func renderTailEnvelope(w io.Writer, msg client.Message, asJSON bool) error {
	if asJSON {
		raw, err := json.Marshal(msg.Envelope)
		if err != nil {
			return fmt.Errorf("marshal envelope: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	ts := msg.Envelope.Ts.Time.Format(time.RFC3339)
	summary := summarizeEnvelope(msg.Envelope)
	if summary == "" {
		printf(w, "[%s] %s  kind=%s\n", ts, msg.Subject, msg.Envelope.Kind)
	} else {
		printf(w, "[%s] %s  kind=%s  %s\n", ts, msg.Subject, msg.Envelope.Kind, summary)
	}
	return nil
}

// summarizeEnvelope produces a one-line summary tailored to the
// envelope's kind. Unknown kinds fall back to a short payload preview so
// the operator at least sees something.
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

// payloadPreview returns a short stringified preview of the payload
// suitable for one-line output. Long payloads are truncated; raw JSON
// stays inline so the operator can still scan structure.
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

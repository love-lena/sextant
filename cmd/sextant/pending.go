package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

const pendingUsage = `usage: sextant pending <verb> [args...]

Verbs:
  list [--since 1h]                       Snapshot of unanswered user_input requests.
  answer <request_id> "<answer>"          Publish an answer.
  defer <request_id>                      Defer to operator (publishes a defer response).
  escalate <request_id> --to <agent>      Escalate to another agent.

Every verb supports --json.`

// runPending dispatches `sextant pending <verb>`.
func runPending(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, pendingUsage)
		return errUserUsage("missing pending verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		return runPendingList(ctx, rest)
	case "answer":
		return runPendingAnswer(ctx, rest)
	case "defer":
		return runPendingDefer(ctx, rest)
	case "escalate":
		return runPendingEscalate(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, pendingUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, pendingUsage)
		return errUserUsage(fmt.Sprintf("unknown pending verb %q", verb))
	}
}

// pendingRequest is the shape rendered by `pending list`.
type pendingRequest struct {
	RequestID uuid.UUID         `json:"request_id"`
	FromUUID  uuid.UUID         `json:"from_uuid"`
	Question  string            `json:"question"`
	Options   []string          `json:"options,omitempty"`
	Urgency   string            `json:"urgency,omitempty"`
	Context   map[string]string `json:"context,omitempty"`
	Ts        time.Time         `json:"ts"`
}

// runPendingList drains the user_input stream once via DeliverAll
// and prints the requests that don't have a matching response yet.
// The snapshot is bounded by the stream's natural retention (30d per
// pkg/natsboot/layout.go), with the --since flag clamping the lookback.
func runPendingList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant pending list", flag.ContinueOnError)
	var since time.Duration
	fs.DurationVar(&since, "since", time.Hour, "lookback window (e.g. 1h, 24h)")
	opts, _, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	// Subscribe to the entire user_input.> namespace from the start so
	// we see both requests and responses for matching. We cut over to
	// "no new messages in 250ms" to decide the snapshot is complete —
	// the stream is JetStream-bounded so a stale tail can't dominate.
	msgs, err := cli.Subscribe(ctx, "user_input.>", client.WithDeliverAll())
	if err != nil {
		return fmt.Errorf("subscribe user_input.>: %w", err)
	}

	cutoff := time.Now().Add(-since)
	requests := map[uuid.UUID]pendingRequest{}
	answered := map[uuid.UUID]bool{}

	// Drain with a quiet-period timer; spec says the snapshot is
	// "bounded" — 500ms of silence is the practical end.
	quiet := time.NewTimer(500 * time.Millisecond)
	defer quiet.Stop()
drainloop:
	for {
		select {
		case <-ctx.Done():
			break drainloop
		case msg, ok := <-msgs:
			if !ok {
				break drainloop
			}
			if msg.Err != nil {
				continue
			}
			// Reset quiet timer on every message.
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(500 * time.Millisecond)

			if msg.Timestamp.Before(cutoff) {
				continue
			}
			switch msg.Envelope.Kind {
			case sextantproto.KindUserInputRequest:
				var p sextantproto.UserInputRequestPayload
				if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
					continue
				}
				requests[p.RequestID] = pendingRequest{
					RequestID: p.RequestID,
					FromUUID:  p.FromUUID,
					Question:  p.Question,
					Options:   p.Options,
					Urgency:   p.Urgency,
					Context:   p.Context,
					Ts:        msg.Timestamp,
				}
			case sextantproto.KindUserInputResponse:
				var p sextantproto.UserInputResponsePayload
				if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
					continue
				}
				answered[p.RequestID] = true
			}
		case <-quiet.C:
			break drainloop
		}
	}

	// Project to unanswered.
	unanswered := make([]pendingRequest, 0, len(requests))
	for id, r := range requests {
		if answered[id] {
			continue
		}
		unanswered = append(unanswered, r)
	}

	if opts.asJSON {
		return writeJSON(os.Stdout, unanswered)
	}
	if len(unanswered) == 0 {
		println(os.Stdout, "no pending requests")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	printf(tw, "REQUEST_ID\tFROM\tURGENCY\tQUESTION\n")
	for _, r := range unanswered {
		question := r.Question
		if len(question) > 60 {
			question = question[:60] + "…"
		}
		printf(tw, "%s\t%s\t%s\t%s\n", r.RequestID, r.FromUUID, r.Urgency, question)
	}
	return tw.Flush()
}

// runPendingAnswer publishes a UserInputResponsePayload with
// decision=answer on user_input.responses.<request_id>.
func runPendingAnswer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant pending answer", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errUserUsage(`sextant pending answer <request_id> "<answer>"`)
	}
	rid, err := uuid.Parse(rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("request_id: %v", err))
	}
	if strings.TrimSpace(rest[1]) == "" {
		return errUserUsage("answer must be non-empty")
	}
	return publishUserInputResponse(ctx, opts, rid, sextantproto.UserInputResponsePayload{
		RequestID: rid,
		Decision:  sextantproto.InputAnswer,
		Answer:    rest[1],
	})
}

// runPendingDefer publishes a UserInputResponsePayload with
// decision=defer.
func runPendingDefer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant pending defer", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant pending defer <request_id>")
	}
	rid, err := uuid.Parse(rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("request_id: %v", err))
	}
	return publishUserInputResponse(ctx, opts, rid, sextantproto.UserInputResponsePayload{
		RequestID: rid,
		Decision:  sextantproto.InputDefer,
	})
}

// runPendingEscalate publishes a UserInputResponsePayload with
// decision=escalate and escalate_to set.
func runPendingEscalate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant pending escalate", flag.ContinueOnError)
	var to string
	fs.StringVar(&to, "to", "", "agent UUID or name to escalate to")
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant pending escalate <request_id> --to <agent>")
	}
	if strings.TrimSpace(to) == "" {
		return errUserUsage("--to is required")
	}
	rid, err := uuid.Parse(rest[0])
	if err != nil {
		return errUserUsage(fmt.Sprintf("request_id: %v", err))
	}
	target := to
	return publishUserInputResponse(ctx, opts, rid, sextantproto.UserInputResponsePayload{
		RequestID:  rid,
		Decision:   sextantproto.InputEscalate,
		EscalateTo: &target,
	})
}

// publishUserInputResponse builds and publishes the response envelope.
// Returns nil on success; cleanly closes the client.
func publishUserInputResponse(ctx context.Context, opts commonOpts, requestID uuid.UUID, payload sextantproto.UserInputResponsePayload) error {
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "operator"}
	env := sextantproto.NewEnvelope(sextantproto.KindUserInputResponse, from, raw)
	subject := "user_input.responses." + requestID.String()
	if err := cli.Publish(ctx, subject, env); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, payload)
	}
	println(os.Stdout, "ok")
	return nil
}

// _ keep the io import alive — used in conversation_test.go via
// streamConversation. The pending list path uses os.Stdout only.
var _ = io.Discard

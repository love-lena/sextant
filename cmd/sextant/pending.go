// pending.go owns `sextant pending <verb>` — list / answer / defer /
// escalate user-input requests.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// newPendingCmd builds the `sextant pending` parent command.
func newPendingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List/answer/defer/escalate user-input requests",
	}
	cmd.AddCommand(newPendingListCmd())
	cmd.AddCommand(newPendingAnswerCmd())
	cmd.AddCommand(newPendingDeferCmd())
	cmd.AddCommand(newPendingEscalateCmd())
	return cmd
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

func newPendingListCmd() *cobra.Command {
	var since time.Duration
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Snapshot of unanswered user_input requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			msgs, err := cli.Subscribe(ctx, "user_input.>", client.WithDeliverAll())
			if err != nil {
				return fmt.Errorf("subscribe user_input.>: %w", err)
			}

			cutoff := time.Now().Add(-since)
			requests := map[uuid.UUID]pendingRequest{}
			answered := map[uuid.UUID]bool{}

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

			unanswered := make([]pendingRequest, 0, len(requests))
			for id, r := range requests {
				if answered[id] {
					continue
				}
				unanswered = append(unanswered, r)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				if err := writeJSON(cmd, out, unanswered); err != nil {
					return err
				}
				if len(unanswered) == 0 {
					return errNoResults
				}
				return nil
			}
			if len(unanswered) == 0 {
				if _, err := fmt.Fprintln(out, "no pending requests"); err != nil {
					return err
				}
				return errNoResults
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			println(tw, "REQUEST_ID\tFROM\tURGENCY\tQUESTION")
			for _, r := range unanswered {
				q := r.Question
				if len(q) > 60 {
					q = q[:60] + "…"
				}
				printf(tw, "%s\t%s\t%s\t%s\n", r.RequestID, r.FromUUID, r.Urgency, q)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().DurationVar(&since, "since", time.Hour, "lookback window (e.g. 1h, 24h)")
	addPendingListIFlagFollowUp(cmd)
	return cmd
}

func newPendingAnswerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "answer <request_id> <answer>",
		Short: "Publish an answer to a user-input request",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rid, err := uuid.Parse(args[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("request_id: %v", err))
			}
			if strings.TrimSpace(args[1]) == "" {
				return errUserUsage("answer must be non-empty")
			}
			return publishUserInputResponse(cmd, rid, sextantproto.UserInputResponsePayload{
				RequestID: rid,
				Decision:  sextantproto.InputAnswer,
				Answer:    args[1],
			})
		},
	}
}

func newPendingDeferCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "defer <request_id>",
		Short: "Defer a user-input request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rid, err := uuid.Parse(args[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("request_id: %v", err))
			}
			return publishUserInputResponse(cmd, rid, sextantproto.UserInputResponsePayload{
				RequestID: rid,
				Decision:  sextantproto.InputDefer,
			})
		},
	}
}

func newPendingEscalateCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "escalate <request_id>",
		Short: "Escalate a user-input request to another agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(to) == "" {
				return errUserUsage("--to is required")
			}
			rid, err := uuid.Parse(args[0])
			if err != nil {
				return errUserUsage(fmt.Sprintf("request_id: %v", err))
			}
			target := to
			return publishUserInputResponse(cmd, rid, sextantproto.UserInputResponsePayload{
				RequestID:  rid,
				Decision:   sextantproto.InputEscalate,
				EscalateTo: &target,
			})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "agent UUID or name to escalate to")
	return cmd
}

func publishUserInputResponse(cmd *cobra.Command, requestID uuid.UUID, payload sextantproto.UserInputResponsePayload) error {
	ctx := cmd.Context()
	cli, _, err := connectAgent(ctx, globalFlags.configDir)
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
	out := cmd.OutOrStdout()
	if globalFlags.asJSON {
		return writeJSON(cmd, out, payload)
	}
	_, err = fmt.Fprintln(out, "ok")
	return err
}

// ctx kept os reference (file may transitively use it from tests).
var (
	_ = context.Background
	_ = os.Stdout
)

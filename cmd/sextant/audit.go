// audit.go owns `sextant audit <verb>` — query the ClickHouse audit
// table or live-tail the audit.> NATS subjects.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query or tail the audit log",
	}
	cmd.AddCommand(newAuditQueryCmd())
	cmd.AddCommand(newAuditTailCmd())
	return cmd
}

func newAuditQueryCmd() *cobra.Command {
	var (
		since  time.Duration
		actor  string
		action string
		agent  string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the ClickHouse audit table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			req := sextantproto.QueryAuditRequest{
				Filter: sextantproto.QueryAuditFilter{
					Actor:  actor,
					Action: action,
				},
				TimeRange: sextantproto.TimeRange{Since: time.Now().Add(-since)},
				Limit:     limit,
			}
			if agent != "" {
				id, err := uuid.Parse(agent)
				if err != nil {
					return errUserUsage(fmt.Sprintf("--agent: %v", err))
				}
				req.Filter.AgentUUID = id
			}
			var resp sextantproto.QueryAuditResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbQueryAudit, req, &resp); err != nil {
				return fmt.Errorf("query_audit: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(out, resp)
			}
			if len(resp.Rows) == 0 {
				_, err := fmt.Fprintln(out, "no audit rows")
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "TS\tACTOR\tACTION\tRESULT\tCAP")
			for _, r := range resp.Rows {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					r.Ts.Format(time.RFC3339), r.Actor, r.Action, r.Result, r.CapabilityRequired)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().DurationVar(&since, "since", time.Hour, "lookback window (e.g. 1h)")
	cmd.Flags().StringVar(&actor, "actor", "", "filter by actor")
	cmd.Flags().StringVar(&action, "action", "", "filter by action")
	cmd.Flags().StringVar(&agent, "agent", "", "filter by agent UUID")
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows (default 1000, max 10000)")
	return cmd
}

func newAuditTailCmd() *cobra.Command {
	var subject string
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Live subscribe to audit.> on NATS",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !strings.HasPrefix(subject, "audit.") {
				return errUserUsage("--filter must be under audit.>")
			}
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			msgs, err := cli.Subscribe(ctx, subject)
			if err != nil {
				return fmt.Errorf("subscribe %s: %w", subject, err)
			}
			out := cmd.OutOrStdout()
			for {
				select {
				case <-ctx.Done():
					return nil
				case msg, ok := <-msgs:
					if !ok {
						return nil
					}
					if msg.Err != nil {
						fmt.Fprintf(os.Stderr, "[decode error seq=%d]: %v\n", msg.StreamSeq, msg.Err)
						continue
					}
					if globalFlags.asJSON {
						raw, _ := json.Marshal(msg.Envelope)
						fmt.Fprintln(out, string(raw))
						_ = msg.Ack()
						continue
					}
					renderAuditEnvelope(out, msg)
					_ = msg.Ack()
				}
			}
		},
	}
	cmd.Flags().StringVar(&subject, "filter", "audit.>", "NATS subject filter under audit.>")
	return cmd
}

var _ = os.Stderr // keep `os` import for the tail error path

func renderAuditEnvelope(w io.Writer, msg client.Message) {
	var p sextantproto.AuditPayload
	if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
		fmt.Fprintf(w, "%s [audit] (undecodable payload)\n", msg.Envelope.Ts.Format(time.RFC3339))
		return
	}
	fmt.Fprintf(w, "%s actor=%s action=%s result=%s cap=%s\n",
		msg.Envelope.Ts.Format(time.RFC3339),
		p.Actor, p.Action, p.Result, p.CapabilityRequired)
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

const auditUsage = `usage: sextant audit <verb> [args...]

Verbs:
  query [--since 1h] [--actor X] [--action spawn] [--agent UUID] [--json]
         Query the ClickHouse audit table.
  tail [--filter SUBJECT] [--json]
         Live subscribe to audit.> on NATS.`

func runAudit(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, auditUsage)
		return errUserUsage("missing audit verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "query":
		return runAuditQuery(ctx, rest)
	case "tail":
		return runAuditTail(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, auditUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, auditUsage)
		return errUserUsage(fmt.Sprintf("unknown audit verb %q", verb))
	}
}

func runAuditQuery(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant audit query", flag.ContinueOnError)
	var (
		since  time.Duration
		actor  string
		action string
		agent  string
		limit  int
	)
	fs.DurationVar(&since, "since", time.Hour, "lookback window (e.g. 1h)")
	fs.StringVar(&actor, "actor", "", "filter by actor (UUID for agents, 'operator' for operator)")
	fs.StringVar(&action, "action", "", "filter by action (e.g. rpc.spawn_agent)")
	fs.StringVar(&agent, "agent", "", "filter by agent UUID")
	fs.IntVar(&limit, "limit", 0, "max rows (default 1000, max 10000)")
	opts, _, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
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
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	if len(resp.Rows) == 0 {
		println(os.Stdout, "no audit rows")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	printf(tw, "TS\tACTOR\tACTION\tRESULT\tCAP\n")
	for _, r := range resp.Rows {
		printf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.Ts.Format(time.RFC3339), r.Actor, r.Action, r.Result, r.CapabilityRequired)
	}
	return tw.Flush()
}

func runAuditTail(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant audit tail", flag.ContinueOnError)
	var subject string
	fs.StringVar(&subject, "filter", "audit.>", "NATS subject filter under audit.>")
	opts, _, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(subject, "audit.") {
		return errUserUsage("--filter must be under audit.>")
	}
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	msgs, err := cli.Subscribe(ctx, subject)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			if msg.Err != nil {
				printf(os.Stderr, "[decode error seq=%d]: %v\n", msg.StreamSeq, msg.Err)
				continue
			}
			if opts.asJSON {
				raw, _ := json.Marshal(msg.Envelope)
				println(os.Stdout, string(raw))
				_ = msg.Ack()
				continue
			}
			renderAuditEnvelope(os.Stdout, msg)
			_ = msg.Ack()
		}
	}
}

func renderAuditEnvelope(w *os.File, msg client.Message) {
	var p sextantproto.AuditPayload
	if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
		printf(w, "%s [audit] (undecodable payload)\n", msg.Envelope.Ts.Format(time.RFC3339))
		return
	}
	printf(w, "%s actor=%s action=%s result=%s cap=%s\n",
		msg.Envelope.Ts.Format(time.RFC3339),
		p.Actor, p.Action, p.Result, p.CapabilityRequired)
}

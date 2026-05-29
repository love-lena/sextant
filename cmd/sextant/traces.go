// traces.go owns `sextant traces show <trace_id>`.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/traces"
)

func newTracesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traces",
		Short: "Render distributed traces by trace_id",
	}
	cmd.AddCommand(newTracesShowCmd())
	return cmd
}

func newTracesShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <trace_id>",
		Short: "Render a distributed trace as a span tree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, _, err := connectAgent(ctx, globalFlags.configDir)
			if err != nil {
				return err
			}
			defer cli.Close() //nolint:errcheck // best-effort close

			var resp sextantproto.QueryTraceResponse
			rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := cli.RPC(rpcCtx, rpc.VerbQueryTrace,
				sextantproto.QueryTraceRequest{TraceID: args[0]}, &resp); err != nil {
				return fmt.Errorf("query_trace: %w", err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, resp)
			}
			return renderSpanTree(out, resp.Spans)
		},
	}
	addTracesShowIFlag(cmd)
	return cmd
}

// renderSpanTree prints the span tree to stdout. The tree projection is
// shared with the interactive `traces show -i` surface via
// pkg/tui/traces (BuildSpanTree + FlattenVisible) so the layout logic
// lives in exactly one place.
func renderSpanTree(w io.Writer, spans []sextantproto.TraceSpan) error {
	if len(spans) == 0 {
		_, err := fmt.Fprintln(w, "no spans")
		return err
	}
	for _, row := range traces.FlattenVisible(traces.BuildSpanTree(spans), nil) {
		printSpanRow(w, row)
	}
	return nil
}

func printSpanRow(w io.Writer, r traces.Row) {
	indent := strings.Repeat("  ", r.Depth)
	dur := time.Duration(r.Span.DurationNanos)
	status := ""
	if sc := r.Span.StatusCode; sc != "" && sc != "STATUS_CODE_OK" && sc != "OK" {
		status = " [" + sc + "]"
	}
	printf(w, "%s%s%s (%s) %s\n",
		indent, r.Span.SpanName, status, dur, r.Span.SpanID)
}

// traces.go owns `sextant traces show <trace_id>`.
package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
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
	return &cobra.Command{
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
				return writeJSON(out, resp)
			}
			return renderSpanTree(out, resp.Spans)
		},
	}
}

// renderSpanTree projects spans into a parent→children index, then
// walks every root in Timestamp order.
func renderSpanTree(w io.Writer, spans []sextantproto.TraceSpan) error {
	if len(spans) == 0 {
		_, err := fmt.Fprintln(w, "no spans")
		return err
	}
	children := map[string][]sextantproto.TraceSpan{}
	known := map[string]bool{}
	for _, s := range spans {
		known[s.SpanID] = true
	}
	roots := make([]sextantproto.TraceSpan, 0)
	for _, s := range spans {
		if s.ParentSpanID == "" || !known[s.ParentSpanID] {
			roots = append(roots, s)
			continue
		}
		children[s.ParentSpanID] = append(children[s.ParentSpanID], s)
	}
	for k := range children {
		sort.Slice(children[k], func(i, j int) bool {
			return children[k][i].Timestamp.Before(children[k][j].Timestamp)
		})
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Timestamp.Before(roots[j].Timestamp)
	})
	type frame struct {
		span  sextantproto.TraceSpan
		depth int
	}
	stack := make([]frame, 0, len(spans))
	for i := len(roots) - 1; i >= 0; i-- {
		stack = append(stack, frame{span: roots[i], depth: 0})
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		printSpan(w, top.span, top.depth)
		kids, ok := children[top.span.SpanID]
		if !ok {
			continue
		}
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, frame{span: kids[i], depth: top.depth + 1})
		}
	}
	return nil
}

func printSpan(w io.Writer, s sextantproto.TraceSpan, depth int) {
	indent := strings.Repeat("  ", depth)
	dur := time.Duration(s.DurationNanos)
	status := ""
	if s.StatusCode != "" && s.StatusCode != "STATUS_CODE_OK" && s.StatusCode != "OK" {
		status = " [" + s.StatusCode + "]"
	}
	printf(w, "%s%s%s (%s) %s\n",
		indent, s.SpanName, status, dur, s.SpanID)
}

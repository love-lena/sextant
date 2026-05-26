package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

const tracesUsage = `usage: sextant traces show <trace_id> [--json]

Render a distributed trace as a span tree. Spans are projected into a
tree by ParentSpanId, sorted by Timestamp ASC within each level.`

func runTraces(ctx context.Context, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, tracesUsage)
		return errUserUsage("missing traces verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "show":
		return runTracesShow(ctx, rest)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, tracesUsage)
		return nil
	default:
		_, _ = fmt.Fprintln(os.Stderr, tracesUsage)
		return errUserUsage(fmt.Sprintf("unknown traces verb %q", verb))
	}
}

func runTracesShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant traces show", flag.ContinueOnError)
	opts, rest, err := parseCommonOpts(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUserUsage("sextant traces show <trace_id>")
	}
	traceID := rest[0]
	cli, _, err := connectAgent(ctx, opts.configDir)
	if err != nil {
		return err
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	var resp sextantproto.QueryTraceResponse
	rpcCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := cli.RPC(rpcCtx, rpc.VerbQueryTrace,
		sextantproto.QueryTraceRequest{TraceID: traceID}, &resp); err != nil {
		return fmt.Errorf("query_trace: %w", err)
	}
	if opts.asJSON {
		return writeJSON(os.Stdout, resp)
	}
	return renderSpanTree(os.Stdout, resp.Spans)
}

// renderSpanTree projects spans into a parent→children index, then
// walks every root in Timestamp order. The walker is iterative so a
// deep trace doesn't blow the stack.
func renderSpanTree(w io.Writer, spans []sextantproto.TraceSpan) error {
	if len(spans) == 0 {
		println(w, "no spans")
		return nil
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

	// Walk iteratively. Each frame is {span, depth}.
	type frame struct {
		span  sextantproto.TraceSpan
		depth int
	}
	stack := make([]frame, 0, len(spans))
	// Push in reverse so the first root pops first (DFS pre-order).
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

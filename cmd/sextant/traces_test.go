package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestRenderSpanTreeNests proves the tree projection: a root span
// with two children renders the children indented one level under
// the root.
func TestRenderSpanTreeNests(t *testing.T) {
	now := time.Now()
	spans := []sextantproto.TraceSpan{
		{
			TraceID: "abc", SpanID: "root", SpanName: "rpc.spawn_agent",
			Timestamp: now, DurationNanos: int64(200 * time.Millisecond),
		},
		{
			TraceID: "abc", SpanID: "child-a", ParentSpanID: "root",
			SpanName: "containers.run", Timestamp: now.Add(10 * time.Millisecond),
			DurationNanos: int64(100 * time.Millisecond),
		},
		{
			TraceID: "abc", SpanID: "child-b", ParentSpanID: "root",
			SpanName: "kv.put", Timestamp: now.Add(50 * time.Millisecond),
			DurationNanos: int64(5 * time.Millisecond),
		},
	}
	var buf bytes.Buffer
	if err := renderSpanTree(&buf, spans); err != nil {
		t.Fatalf("renderSpanTree: %v", err)
	}
	out := buf.String()
	rootLine := "rpc.spawn_agent"
	if !strings.Contains(out, rootLine) {
		t.Fatalf("output missing root: %q", out)
	}
	// Child lines are indented two spaces.
	if !strings.Contains(out, "  containers.run") {
		t.Errorf("output missing indented child: %q", out)
	}
	if !strings.Contains(out, "  kv.put") {
		t.Errorf("output missing indented child: %q", out)
	}
	// Order check: containers.run (10ms) before kv.put (50ms).
	containersAt := strings.Index(out, "containers.run")
	kvAt := strings.Index(out, "kv.put")
	if containersAt == -1 || kvAt == -1 || containersAt >= kvAt {
		t.Errorf("expected containers.run before kv.put: %q", out)
	}
}

func TestRenderSpanTreeHandlesEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderSpanTree(&buf, nil); err != nil {
		t.Fatalf("renderSpanTree: %v", err)
	}
	if !strings.Contains(buf.String(), "no spans") {
		t.Errorf("empty input: %q", buf.String())
	}
}

func TestRenderSpanTreeOrphansBecomeRoots(t *testing.T) {
	// A span pointing at a parent that doesn't exist in the result
	// set is treated as a root so the tree still renders.
	spans := []sextantproto.TraceSpan{
		{
			TraceID: "abc", SpanID: "orphan", ParentSpanID: "missing",
			SpanName: "orphaned.span", Timestamp: time.Now(),
		},
	}
	var buf bytes.Buffer
	if err := renderSpanTree(&buf, spans); err != nil {
		t.Fatalf("renderSpanTree: %v", err)
	}
	if !strings.Contains(buf.String(), "orphaned.span") {
		t.Errorf("orphan not rendered: %q", buf.String())
	}
	// Not indented — orphan promoted to root.
	if strings.Contains(buf.String(), "  orphaned.span") {
		t.Errorf("orphan got indented: %q", buf.String())
	}
}

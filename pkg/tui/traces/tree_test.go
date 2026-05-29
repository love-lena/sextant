package traces

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func span(id, parent string, ts int) sextantproto.TraceSpan {
	return sextantproto.TraceSpan{
		SpanID:       id,
		ParentSpanID: parent,
		SpanName:     id,
		Timestamp:    time.Unix(int64(ts), 0),
	}
}

func TestBuildSpanTreeOrdersRootsAndChildren(t *testing.T) {
	spans := []sextantproto.TraceSpan{
		span("root", "", 0),
		span("b", "root", 2),
		span("a", "root", 1),
		span("orphan", "missing", 3), // parent absent → treated as root
	}
	roots := BuildSpanTree(spans)
	if len(roots) != 2 {
		t.Fatalf("roots = %d, want 2 (root + orphan)", len(roots))
	}
	if roots[0].Span.SpanID != "root" || roots[1].Span.SpanID != "orphan" {
		t.Fatalf("root order = %s,%s; want root,orphan", roots[0].Span.SpanID, roots[1].Span.SpanID)
	}
	kids := roots[0].Children
	if len(kids) != 2 || kids[0].Span.SpanID != "a" || kids[1].Span.SpanID != "b" {
		t.Fatalf("children mis-ordered: %+v", kids)
	}
}

func TestFlattenVisibleRespectsCollapse(t *testing.T) {
	roots := BuildSpanTree([]sextantproto.TraceSpan{
		span("root", "", 0), span("child", "root", 1),
	})
	rows := FlattenVisible(roots, map[string]bool{"root": true})
	if len(rows) != 1 || rows[0].Span.SpanID != "root" {
		t.Fatalf("collapsed flatten = %+v, want [root]", rows)
	}
	if !rows[0].HasChildren {
		t.Fatal("collapsed root should still report HasChildren")
	}
	rows = FlattenVisible(roots, map[string]bool{})
	if len(rows) != 2 {
		t.Fatalf("expanded flatten = %d rows, want 2", len(rows))
	}
	if rows[1].Depth != 1 {
		t.Fatalf("child depth = %d, want 1", rows[1].Depth)
	}
}

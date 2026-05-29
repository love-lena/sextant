package traces

import (
	"sort"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Node is one span plus its ordered children. Roots are spans with no
// parent, or whose parent is absent from the result set.
type Node struct {
	Span     sextantproto.TraceSpan
	Children []*Node
}

// Row is a flattened, depth-annotated node for list rendering.
type Row struct {
	Span        sextantproto.TraceSpan
	Depth       int
	HasChildren bool
}

// BuildSpanTree projects spans into a root-ordered tree. Roots and
// children are ordered by Timestamp ascending. Shared with the static
// `sextant traces show` stdout renderer so the projection lives in one
// place.
func BuildSpanTree(spans []sextantproto.TraceSpan) []*Node {
	known := make(map[string]bool, len(spans))
	nodes := make(map[string]*Node, len(spans))
	for i := range spans {
		known[spans[i].SpanID] = true
		nodes[spans[i].SpanID] = &Node{Span: spans[i]}
	}
	var roots []*Node
	for i := range spans {
		n := nodes[spans[i].SpanID]
		parent := spans[i].ParentSpanID
		if parent == "" || !known[parent] {
			roots = append(roots, n)
			continue
		}
		p := nodes[parent]
		if p == nil {
			// Unreachable in practice: known[parent] is true iff parent's
			// span was in the input, which means nodes[parent] was set in
			// the first loop. The guard satisfies nilaway's nil-flow check.
			roots = append(roots, n)
			continue
		}
		p.Children = append(p.Children, n)
	}
	byTS := func(list []*Node) {
		sort.SliceStable(list, func(i, j int) bool {
			return list[i].Span.Timestamp.Before(list[j].Span.Timestamp)
		})
	}
	byTS(roots)
	for _, n := range nodes {
		byTS(n.Children)
	}
	return roots
}

// FlattenVisible walks the tree depth-first, skipping the subtrees of
// nodes whose SpanID is in collapsed. Returns rows in render order.
func FlattenVisible(roots []*Node, collapsed map[string]bool) []Row {
	var out []Row
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		out = append(out, Row{Span: n.Span, Depth: depth, HasChildren: len(n.Children) > 0})
		if collapsed[n.Span.SpanID] {
			return
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

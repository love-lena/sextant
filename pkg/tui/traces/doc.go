// Package traces is the Tier 1 component for `sextant traces show <id>
// -i`: an interactive collapse/expand span-tree outline over a
// query_trace result, built on widget.ListPane fed a flattened,
// depth-annotated row slice.
//
// The pure tree projection (BuildSpanTree / FlattenVisible) is exported
// so the static `sextant traces show` stdout renderer shares it.
// Resolves slug:feat-tui-traces-component.
package traces

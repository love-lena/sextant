// Package contextview is the Tier 1 component for `sextant agents
// context <agent> -i` (Phase B): a scrollable, tailing viewport over an
// agent's SDK session JSONL, with mode keys 1–6 switching the view
// (raw/conversation/tools/thinking/usage/tree). Built on
// widget.StreamViewport; per-line rendering is shared with the CLI dump
// via pkg/sessionlog.RenderLine.
//
// Named contextview (not context) to avoid clashing with the stdlib
// context package at every import site. Completes
// plans/issues/feat-agents-context-view.md.
package contextview

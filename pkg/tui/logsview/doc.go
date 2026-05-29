// Package logsview is the Tier 1 component for `sextant daemon logs -i`:
// a scrollable, tailing viewport over the daemon log file. A thin
// composition of widget.StreamViewport fed a widget.TailSource — the
// canonical "stream surface = StreamViewport + a Source" shape from the
// RFC (plans/rfc-tui-workstream.md, P2).
package logsview

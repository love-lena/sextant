// Package pending is the Tier 1 component for `sextant pending list -i`
// and the dash "pending" pane: a live list of unanswered user_input
// requests, composed from widget.ListPane + a widget.SubscribeSource over
// the `user_input.>` subject.
//
// NOTE: nothing in production publishes user_input *requests* yet (only
// test fixtures do — see plans/rfc-tui-workstream.md §6, Open Q5). This
// surface renders correctly but is empty against a live daemon until an
// agent→operator escalation producer lands. Resolves
// slug:feat-tui-pending-component.
package pending

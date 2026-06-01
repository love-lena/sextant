// events.go owns the `sextant events` parent command — a new noun
// per `slug:feat-cli-resource-verb-cleanup`. Holds the
// `tail` verb (subscribe to an arbitrary NATS subject) and is the
// designated home for future `events pub` / `events sub` debugging
// verbs against subjects + KV. Internal vocab (`pkg/sextantbus`)
// stays; `events` is the operator-facing name.
package main

import (
	"time"

	"github.com/spf13/cobra"
)

// newEventsCmd builds the `sextant events` resource noun.
func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Inspect the NATS event bus (tail subjects, etc.)",
		Long: `Surface NATS bus traffic the same way an operator inspects audit logs.
Today's verbs: tail. Future: pub/sub against arbitrary subjects + KV.`,
	}
	cmd.AddCommand(newEventsTailCmd())
	return cmd
}

// newEventsTailCmd wires `sextant events tail <subject> [--from-seq N] [--for D]`.
// Thin wrapper over pkg/client.Subscribe. Replaces the legacy top-level
// `sextant tail`; the old form is preserved as an alias.
func newEventsTailCmd() *cobra.Command {
	var (
		fromSeq  uint64
		duration time.Duration
	)
	cmd := &cobra.Command{
		Use:   "tail <subject>",
		Short: "Subscribe to an arbitrary NATS subject (wildcards OK)",
		Long: `Subscribe to a NATS subject and print envelopes as they arrive.
Subjects accept NATS wildcards (* matches one token, > matches one or more).
Common patterns:

  agents.>                  every agent's events
  agents.*.lifecycle        lifecycle across all agents
  telemetry.>               OTel firehose
  sextant.system.>          daemon self-management events
  audit.>                   audit log

Default output is one human-readable line per envelope; --json swaps to
raw envelope NDJSON. --from-seq rebinds the consumer at the given
JetStream stream sequence so an operator can gap-fill after a
disconnect. --for bounds the subscription: exit cleanly after that
duration. Useful for capturing a short debug window without Ctrl-C.

Note: a JetStream consumer binds to exactly one stream, so the subject
must resolve to a single stream — a bare > firehose spans every stream
and is not subscribable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			subject := args[0]
			if subject == "" {
				return errUserUsage("subject must be non-empty")
			}
			return runEventsTail(cmd, subject, fromSeq, duration)
		},
	}
	cmd.Flags().Uint64Var(&fromSeq, "from-seq", 0,
		"resume from JetStream stream sequence N")
	cmd.Flags().DurationVar(&duration, "for", 0,
		"exit cleanly after this duration (e.g. 3s, 1m); 0 = run until interrupted")
	return cmd
}

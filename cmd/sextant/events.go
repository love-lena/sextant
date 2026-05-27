// events.go owns the `sextant events` parent command — a new noun
// per `plans/issues/feat-cli-resource-verb-cleanup.md`. Holds the
// `tail` verb (subscribe to an arbitrary NATS subject) and is the
// designated home for future `events pub` / `events sub` debugging
// verbs against subjects + KV. Internal vocab (`pkg/sextantbus`)
// stays; `events` is the operator-facing name.
package main

import (
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

// newEventsTailCmd wires `sextant events tail <subject> [--from-seq N]`.
// Thin wrapper over pkg/client.Subscribe. Replaces the legacy top-level
// `sextant tail`; the old form is preserved as an alias.
func newEventsTailCmd() *cobra.Command {
	var fromSeq uint64
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
disconnect.

Note: a JetStream consumer binds to exactly one stream, so the subject
must resolve to a single stream — a bare > firehose spans every stream
and is not subscribable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			subject := args[0]
			if subject == "" {
				return errUserUsage("subject must be non-empty")
			}
			return runEventsTail(cmd, subject, fromSeq)
		},
	}
	cmd.Flags().Uint64Var(&fromSeq, "from-seq", 0,
		"resume from JetStream stream sequence N")
	return cmd
}

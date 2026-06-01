// aliases.go owns the backwards-compatibility aliases for legacy
// top-level verbs that moved under resource nouns per
// `slug:feat-cli-resource-verb-cleanup`. Each alias prints
// a stderr deprecation note (suppressed under --json) and delegates to
// the new home. Removed one minor release after landing.
package main

import (
	"github.com/spf13/cobra"
)

// newAskAliasCmd — legacy `sextant ask <agent> "<text>"` →
// `sextant agents chat <agent> "<text>"` (one-shot mode).
func newAskAliasCmd() *cobra.Command {
	inner := newAgentsChatCmd()
	cmd := &cobra.Command{
		Use:        "ask <agent> <text>",
		Short:      "(deprecated) one-shot prompt; use `sextant agents chat`",
		Hidden:     true,
		Deprecated: "use `sextant agents chat <agent> \"<text>\"` instead",
		Args:       inner.Args,
		RunE: func(cmd *cobra.Command, args []string) error {
			deprecationNote(cmd, "sextant ask", "sextant agents chat")
			return inner.RunE(cmd, args)
		},
	}
	// Inherit the inner command's flags so the legacy form keeps
	// behaving identically (timeout, json, etc.). Persistent flags
	// from root already propagate, so we only copy local flags.
	cmd.Flags().AddFlagSet(inner.Flags())
	return cmd
}

// newConversationAliasCmd — legacy `sextant conversation <agent>` →
// `sextant agents chat <agent>` (TUI mode).
func newConversationAliasCmd() *cobra.Command {
	inner := newAgentsChatCmd()
	cmd := &cobra.Command{
		Use:        "conversation <agent>",
		Short:      "(deprecated) open chat TUI; use `sextant agents chat`",
		Hidden:     true,
		Deprecated: "use `sextant agents chat <agent>` instead",
		Args:       cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deprecationNote(cmd, "sextant conversation", "sextant agents chat")
			return inner.RunE(cmd, args)
		},
	}
	cmd.Flags().AddFlagSet(inner.Flags())
	return cmd
}

// newTailAliasCmd — legacy `sextant tail <subject>` →
// `sextant events tail <subject>`.
func newTailAliasCmd() *cobra.Command {
	inner := newEventsTailCmd()
	cmd := &cobra.Command{
		Use:        "tail <subject>",
		Short:      "(deprecated) subscribe to a NATS subject; use `sextant events tail`",
		Hidden:     true,
		Deprecated: "use `sextant events tail <subject>` instead",
		Args:       cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deprecationNote(cmd, "sextant tail", "sextant events tail")
			return inner.RunE(cmd, args)
		},
	}
	cmd.Flags().AddFlagSet(inner.Flags())
	return cmd
}

// newExecAliasCmd — legacy `sextant exec <agent> -- <cmd>` →
// `sextant agents exec <agent> -- <cmd>`.
func newExecAliasCmd() *cobra.Command {
	inner := newAgentsExecCmd()
	cmd := &cobra.Command{
		Use:        "exec <agent_uuid> -- <cmd> [args...]",
		Short:      "(deprecated) exec into an agent's container; use `sextant agents exec`",
		Hidden:     true,
		Deprecated: "use `sextant agents exec <agent_uuid> -- <cmd>` instead",
		Args:       inner.Args,
		RunE: func(cmd *cobra.Command, args []string) error {
			deprecationNote(cmd, "sextant exec", "sextant agents exec")
			return inner.RunE(cmd, args)
		},
	}
	cmd.Flags().AddFlagSet(inner.Flags())
	return cmd
}

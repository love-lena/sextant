// destructive.go owns the helper for the destructive-op flag bundle
// per `plans/issues/feat-cli-destructive-op-flags.md` — every verb
// that mutates durable state irreversibly gets `--dry-run` and
// `--yes` via `destructiveFlags`. The TTY+Huh interactive confirm
// variant is filed as a follow-up; for now operators must pass
// `--yes` explicitly (or use `--dry-run` to preview).
package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// errDestructiveNoYes is the sentinel returned when a destructive op
// is invoked without --yes. The CLI driver maps this to exit 1
// (user error) rather than 2 (system error).
var errDestructiveNoYes = errors.New("destructive op requires --yes")

// destructiveFlags is the small bundle attached to every destructive
// verb. Use via `flags := newDestructiveFlags(cmd)` in the verb's
// builder; then `flags.confirm(cmd, action)` at the top of RunE.
type destructiveFlags struct {
	dryRun bool
	yes    bool
}

// newDestructiveFlags wires the standard --dry-run + --yes flags on
// the given cobra command and returns a handle the verb's RunE
// uses to gate the action.
func newDestructiveFlags(cmd *cobra.Command) *destructiveFlags {
	f := &destructiveFlags{}
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false,
		"print what would happen and exit 0 (no RPC issued)")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false,
		"confirm the destructive action without prompting")
	return f
}

// confirm gates the destructive action. Returns:
//   - (true, nil)   — proceed (operator passed --yes)
//   - (false, nil)  — short-circuit (operator passed --dry-run; action printed)
//   - (false, err)  — refuse (no --yes; structured user error naming the flag)
//
// `action` describes the verb in operator terms — e.g. "kill agent
// alpha (a3b9…)". Embedded in both the dry-run line and the error
// message so the operator sees the exact resource being targeted.
func (f *destructiveFlags) confirm(cmd *cobra.Command, action string) (bool, error) {
	if f.dryRun {
		fmt.Fprintf(cmd.ErrOrStderr(), "[dry-run] would %s\n", action)
		return false, nil
	}
	if !f.yes {
		return false, fmt.Errorf("%w: %s\nre-run with --yes to proceed (or --dry-run to preview)",
			errDestructiveNoYes, action)
	}
	return true, nil
}

// destructive.go owns the helper for the destructive-op flag bundle
// per `plans/issues/feat-cli-destructive-op-flags.md` — every verb
// that mutates durable state irreversibly gets `--dry-run` and
// `--yes` via `destructiveFlags`. On an interactive terminal an
// operator who omits both gets a `huh.NewConfirm()` prompt
// (per `plans/issues/feat-cli-huh-interactive-confirm.md`); on a
// non-TTY caller (pipe, CI, redirect) the prompt is skipped and
// the helper returns `errDestructiveNoYes` so scripts fail loud.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// errDestructiveNoYes is the sentinel returned when a destructive op
// is invoked without --yes on a non-TTY stdin. The CLI driver maps
// this to exit 1 (user error) rather than 2 (system error).
var errDestructiveNoYes = errors.New("destructive op requires --yes")

// destructiveFlags is the small bundle attached to every destructive
// verb. Use via `flags := newDestructiveFlags(cmd)` in the verb's
// builder; then `flags.confirm(cmd, action)` at the top of RunE.
//
// `isTTY` and `prompt` are seams for tests — production code leaves
// them nil and the helper falls back to `isatty` + a real huh form.
type destructiveFlags struct {
	dryRun bool
	yes    bool

	// isTTY reports whether stdin is interactive. Defaults to
	// `isatty.IsTerminal(os.Stdin.Fd())` when nil.
	isTTY func() bool
	// prompt renders the interactive confirm and returns the
	// operator's answer. Defaults to a real huh.NewConfirm form
	// when nil. `title` is the short action label, `description`
	// is the same `action` string the dry-run + error paths use.
	prompt func(title, description string) (bool, error)
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
//   - (true, nil)   — proceed (operator passed --yes, or answered Yes at the TTY prompt)
//   - (false, nil)  — short-circuit (operator passed --dry-run; or answered No at the TTY prompt)
//   - (false, err)  — refuse (no --yes on a non-TTY stdin; structured user error naming the flag)
//
// `action` describes the verb in operator terms — e.g. "stop agent
// alpha (a3b9…)". Embedded in the dry-run line, the TTY prompt's
// description, and the non-TTY error so the operator sees the exact
// resource being targeted.
func (f *destructiveFlags) confirm(cmd *cobra.Command, action string) (bool, error) {
	if f.dryRun {
		printf(cmd.ErrOrStderr(), "[dry-run] would %s\n", action)
		return false, nil
	}
	if f.yes {
		return true, nil
	}
	if !f.tty() {
		return false, fmt.Errorf("%w: %s\nre-run with --yes to proceed (or --dry-run to preview)",
			errDestructiveNoYes, action)
	}
	ok, err := f.runPrompt("Proceed?", action)
	if err != nil {
		return false, fmt.Errorf("confirm: %w", err)
	}
	if !ok {
		printf(cmd.ErrOrStderr(), "aborted: %s\n", action)
		return false, nil
	}
	return true, nil
}

// tty returns whether stdin is an interactive terminal. Tests stub
// this via the `isTTY` field; production falls back to isatty.
func (f *destructiveFlags) tty() bool {
	if f.isTTY != nil {
		return f.isTTY()
	}
	return isatty.IsTerminal(os.Stdin.Fd())
}

// runPrompt renders the interactive confirm. Tests stub this via
// the `prompt` field; production falls back to a real huh form.
func (f *destructiveFlags) runPrompt(title, description string) (bool, error) {
	if f.prompt != nil {
		return f.prompt(title, description)
	}
	var answer bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Description(description).
			Affirmative("Yes").
			Negative("No").
			Value(&answer),
	))
	if err := form.Run(); err != nil {
		return false, err
	}
	return answer, nil
}

// root.go owns the Cobra command tree for the sextant CLI. Fang styles
// help/errors/version; charmbracelet/log handles diagnostic output.
//
// Conventions: see `conventions/tui-conventions.md` § "Tier 0: CLI base".
// Migration ticket: `slug:feat-cli-cobra-fang-migration`.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	charmlog "github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

// rootFlags holds the persistent flags every command shares. Populated
// by Cobra during parsing and read by command RunE funcs via the shared
// globals below.
type rootFlags struct {
	configDir string
	dataDir   string
	asJSON    bool
	verbose   bool
	noColor   bool
}

// globalFlags is populated by the root command's persistent flag set.
// Subcommand RunE funcs read from it. This is a deliberate compromise:
// keeping the parsed values on a package var avoids threading a config
// struct through every command, at the cost of a single mutable global.
var globalFlags rootFlags

// userLog is the user-facing logger. Writes to stderr (per the convention
// that stdout is data, stderr is messages). Configured by configureLoggers
// once the root command's PersistentPreRun fires.
//
// Held package-level so future verbs (output protocol envelope wave per
// feat-cli-output-protocol-wiring) can route prose through it consistently.
// Currently unused at the call sites — they still use output.go's
// printf/println wrappers — but kept around as the planned home.
//
//nolint:unused // reserved for the output protocol wave (see ticket above)
var userLog = charmlog.NewWithOptions(os.Stderr, charmlog.Options{
	Level:           charmlog.InfoLevel,
	ReportTimestamp: false,
})

// diagLog is the diagnostic logger, gated on the global -v flag. Writes
// to stderr with a `level=...` prefix when enabled, silent otherwise.
var diagLog = charmlog.NewWithOptions(io.Discard, charmlog.Options{
	Level:           charmlog.DebugLevel,
	ReportTimestamp: false,
})

// configureLoggers re-points the diagnostic logger at stderr when -v is
// set, and propagates the no-color preference into both loggers. Called
// from the root command's PersistentPreRun so every subcommand sees the
// configured state.
func configureLoggers(cmd *cobra.Command) {
	if globalFlags.verbose {
		diagLog.SetOutput(cmd.ErrOrStderr())
		diagLog.SetLevel(charmlog.DebugLevel)
	} else {
		diagLog.SetOutput(io.Discard)
	}
	// NO_COLOR env var is respected by lipgloss/termenv automatically;
	// the --no-color flag is operator-explicit, so we set it here too.
	if globalFlags.noColor {
		_ = os.Setenv("NO_COLOR", "1")
	}
}

// newRootCmd builds the full sextant command tree in resource-verb shape.
// Top-level nouns and the documented exceptions (init, doctor, version)
// are wired here; each verb family lives in its own file (agents.go,
// pending.go, ...) and exposes a `newAgentsCmd()` style constructor.
//
// The shape lines up with `specs/cli/commands.md` and the migrations in
// `slug:feat-cli-resource-verb-cleanup`.
func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sextant",
		Short: "Operator CLI for sextant",
		Long: `sextant is the operator CLI for the sextant agent platform.

Commands follow a resource-verb shape: ` + "`sextant agents list`" + `,
` + "`sextant daemon start`" + `, ` + "`sextant pending answer`" + `. ` + "`init`" + `,
` + "`doctor`" + `, and ` + "`version`" + ` are top-level singletons (verbs on the
sextant install itself).`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			configureLoggers(cmd)
		},
	}

	// Persistent flags shared by every command.
	cmd.PersistentFlags().StringVar(&globalFlags.configDir, "config-dir", "",
		"config directory (default ~/.config/sextant)")
	cmd.PersistentFlags().StringVar(&globalFlags.dataDir, "data-dir", "",
		"data directory (default ~/.local/share/sextant)")
	cmd.PersistentFlags().BoolVar(&globalFlags.asJSON, "json", false,
		"emit machine-parseable JSON to stdout")
	cmd.PersistentFlags().BoolVarP(&globalFlags.verbose, "verbose", "v", false,
		"enable diagnostic logging on stderr")
	cmd.PersistentFlags().BoolVar(&globalFlags.noColor, "no-color", false,
		"disable color output (also respected: NO_COLOR env var)")

	// Top-level nouns + documented singletons.
	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newDoctorCmd())
	cmd.AddCommand(newVersionCmd())

	cmd.AddCommand(newAgentsCmd())
	cmd.AddCommand(newPendingCmd())
	cmd.AddCommand(newFilesCmd())
	cmd.AddCommand(newWorktreeCmd())
	cmd.AddCommand(newTemplatesCmd())
	cmd.AddCommand(newAuditCmd())
	cmd.AddCommand(newTracesCmd())

	// New top-level nouns per `feat-cli-resource-verb-cleanup`:
	cmd.AddCommand(newDaemonCmd())
	cmd.AddCommand(newEventsCmd())
	cmd.AddCommand(newThemeCmd())

	// Discovery menu — opens a Huh-driven select listing every Tier 1
	// component registered via pkg/tui/component. Per
	// `slug:feat-sextant-tui-discovery`.
	cmd.AddCommand(newTUICmd())

	// Flagship multi-pane TUI — composes registered Tier 1 components
	// into a Stickers flex layout with BubbleZone mouse regions. Per
	// `slug:feat-sextant-dash-multipane`.
	cmd.AddCommand(newDashCmd())

	// Backwards-compat aliases — each prints a stderr deprecation note
	// pointing at the new home. Removed one minor release after landing.
	cmd.AddCommand(newAskAliasCmd())
	cmd.AddCommand(newConversationAliasCmd())
	cmd.AddCommand(newTailAliasCmd())
	cmd.AddCommand(newExecAliasCmd())
	for _, c := range newDaemonAliasCmds() {
		cmd.AddCommand(c)
	}

	return cmd
}

// deprecationNote prints a one-line deprecation message to stderr unless
// --json was passed (where stdout must stay byte-identical to the new
// form). Used by every backwards-compat alias command.
func deprecationNote(cmd *cobra.Command, oldForm, newForm string) {
	if globalFlags.asJSON {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: %q is deprecated; use %q\n", oldForm, newForm)
}

// exitCodeFor maps a returned error to a process exit code per the
// invariants in specs/cli/commands.md:
//
//	0 — success
//	1 — user error (bad args, usage, agent not found, daemon-not-running)
//	2 — system error (RPC failure, host-dep missing, doctor failures)
//
// Also bubbles up exec's container exit code via exitCodeError so shell
// pipelines see the same exit status they would running the command
// directly inside the container.
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errNoResults) {
		return exitNoResults
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
	var ue usageError
	if errors.As(err, &ue) {
		return exitUser
	}
	if isStatusNotRunningErr(err) {
		return exitUser
	}
	if isDoctorFailureErr(err) {
		return exitSystem
	}
	return exitSystem
}

// shouldSuppressErrorBanner returns true for errors whose verbs print
// their own user-facing context (status's "daemon: not running" line on
// stdout, exec's verbatim stdout/stderr pass-through). The error still
// drives the exit code; we just skip the "sextant: <err>" stderr line
// that fang would render.
func shouldSuppressErrorBanner(err error) bool {
	if errors.Is(err, errNoResults) {
		// The verb already printed its "no agents" / "no pending requests"
		// line on stdout; the banner would noisily restate the sentinel.
		return true
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return true
	}
	if isStatusNotRunningErr(err) {
		return true
	}
	return false
}

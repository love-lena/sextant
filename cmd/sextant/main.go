// sextant is the operator CLI. Built on Cobra (command structure) with
// Fang styling help/errors/version, and charmbracelet/log driving the
// user-facing + diagnostic loggers.
//
// Plan: plans/bootstrap.md#M5 (initial scaffold), then
// plans/issues/feat-cli-cobra-fang-migration.md (framework migration)
// and plans/issues/feat-cli-resource-verb-cleanup.md (resource-verb
// shape + new daemon/events nouns).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/fang"

	"github.com/love-lena/sextant/pkg/cliout"
	"github.com/love-lena/sextant/pkg/version"
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root := newRootCmd()

	opts := []fang.Option{
		fang.WithVersion(version.String()),
		fang.WithErrorHandler(errorBanner),
	}
	if version.GitSHA != "" {
		opts = append(opts, fang.WithCommit(version.GitSHA))
	}

	err := fang.Execute(ctx, root, opts...)
	return exitCodeFor(err)
}

// errorBanner is Fang's WithErrorHandler — it controls how the
// failure surfaces on stderr. Two modes:
//
//   - Plain text (`sextant: <err>`) — the default human surface,
//     matches pre-cobra behavior.
//   - cliout error envelope (`{"error":{"code":..., "message":...}}`) —
//     emitted when `globalFlags.asJSON` is set so `sextant <verb>
//     --json` failures honor the same envelope contract the success
//     path uses. Per the codex adversarial-review finding that flagged
//     the bare-text path as a protocol break.
//
// Verbs that print their own user-facing failure (status's
// "daemon: not running", exec's verbatim pass-through) suppress the
// banner entirely so output stays clean.
func errorBanner(w io.Writer, _ fang.Styles, err error) {
	if err == nil {
		return
	}
	if shouldSuppressErrorBanner(err) {
		return
	}
	if errors.Is(err, errSilentExit) {
		return
	}
	if globalFlags.asJSON {
		code, msg := mapErrorToCode(err)
		_ = cliout.WriteErrorEnvelope(w, code, msg)
		return
	}
	_, _ = fmt.Fprintf(w, "sextant: %s\n", err.Error())
}

// errSilentExit is a sentinel used by commands that already printed
// their own user-facing failure and want main() to skip the banner.
var errSilentExit = errors.New("silent exit")

// Exit codes per specs/cli/commands.md.
//
// 10 (exitNoResults) is the "empty result set" sentinel — distinct
// from real errors so shell loops can branch on it
// (`if foo; then ...; elif [ $? -eq 10 ]; then ...`). Per
// conventions/tui-conventions.md § "Tier 0 → Exit codes".
const (
	exitOK        = 0
	exitUser      = 1
	exitSystem    = 2
	exitNoResults = 10
)

// errNoResults is the sentinel a verb returns when its query returned
// zero rows but no actual error occurred. main() maps this to
// exitNoResults; the verb is responsible for printing the user-visible
// "no results" line first (or nothing, for --json).
var errNoResults = errors.New("no results")

// usageError carries a free-form user-error message. main() inspects via
// errors.As to map it to exit code 1.
type usageError string

func (e usageError) Error() string { return string(e) }

func errUserUsage(msg string) error { return usageError(msg) }

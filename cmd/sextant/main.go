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

// errorBanner is Fang's WithErrorHandler — it controls whether a final
// "Error: <msg>" line is rendered. Verbs that print their own user-facing
// failure (status's "daemon: not running", exec's verbatim pass-through)
// suppress the banner so output stays clean. Everything else falls back
// to a simple `sextant: <err>` line on stderr, matching the pre-cobra
// behavior.
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
	_, _ = fmt.Fprintf(w, "sextant: %s\n", err.Error())
}

// errSilentExit is a sentinel used by commands that already printed
// their own user-facing failure and want main() to skip the banner.
var errSilentExit = errors.New("silent exit")

// Exit codes per specs/cli/commands.md.
const (
	exitOK     = 0
	exitUser   = 1
	exitSystem = 2
)

// usageError carries a free-form user-error message. main() inspects via
// errors.As to map it to exit code 1.
type usageError string

func (e usageError) Error() string { return string(e) }

func errUserUsage(msg string) error { return usageError(msg) }

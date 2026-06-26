// Command sextant-dash is THE dash: the web dash (ADR-0044, ADR-0046). It serves
// the embedded single-page app over a loopback HTTP listener and mints the
// browser tab's bus session credential, so the operator's dashboard is a browser
// tab acting as the operator's own identity — the Go process is just the serve +
// mint engine, holding one bus identity (ADR-0023/0024) with no special privilege
// (ADR-0014, ADR-0008).
//
// The terminal UI is a separate, first-class peer feature reached via the
// sextant-tui binary (ADR-0046); sextant-dash carries no terminal UI. Launching
// is `sextant up` then `sextant components start dash` (or run the binary
// directly); with no identity resolved and a local bus discoverable, it enrolls
// itself (named from $USER; --name overrides) and announces it. An existing
// context is used as-is:
//
//	sextant-dash                             # first run self-enrolls; later runs reuse the context
//	sextant-dash --creds path/to.creds --store path/to/bus-store
//	sextant-dash --context my-context        # resolve creds + URL from a saved context
//	sextant-dash --port 0 --ui ./build       # a dev dash on a free port, custom SPA dir
//
// Flags mirror the operator CLI's connection flags (--creds/--store/--url/
// --context, $SEXTANT_*) plus the serve flags (--port, --allow-origin, --ui,
// --state-file). It prints a 127.0.0.1 URL carrying the per-launch token and
// serves until Ctrl-C / SIGTERM.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/love-lena/sextant/clients/sextant-dash/dashserve"
	"github.com/love-lena/sextant/shared/go/version"
)

func main() {
	fs := flag.NewFlagSet("sextant-dash", flag.ExitOnError)
	flags := dashserve.AddFlags(fs)
	ver := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(os.Args[1:])
	if *ver {
		fmt.Println("sextant-dash " + version.String())
		return
	}

	opts, err := flags.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sextant-dash: %v\n", err)
		os.Exit(1)
	}

	// A signal-bound context so Ctrl-C / SIGTERM winds the serve loop down.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dashserve.Run(ctx, opts, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "sextant-dash: %v\n", err)
		os.Exit(1)
	}
}

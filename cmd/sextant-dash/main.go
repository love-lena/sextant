// Command sextant-dash is the dash: a human-UI client that assembles the M4
// pane-surfaces — presence, the message stream, and an artifact reader — through
// the layout engine into a cockpit, holding one bus identity (ADR-0023). It is
// its own client binary, just another client over the SDK with no special
// privilege (ADR-0014, ADR-0008); `sextant dash` is a thin alias that delegates
// to the same shared dash.Run.
//
// Run it under an identity the bus minted:
//
//	sextant-dash --creds path/to.creds --store path/to/bus-store
//	sextant-dash --context my-context        # resolve creds + URL from a saved context
//	sextant dash                             # the alias, same flags
//
// Flags mirror the operator CLI's connection flags (--creds/--store/--url/
// --context, $SEXTANT_*) plus the dash's own (--theme, --config, --topic,
// --artifact). The cockpit is the default assembly; panes toggle and swap and
// detail opens on demand from the layout (the keymap's o/d/p, arrows, enter/esc).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/love-lena/sextant/internal/dash"
)

func main() {
	fs := flag.NewFlagSet("sextant-dash", flag.ExitOnError)
	flags := dash.AddFlags(fs)
	_ = fs.Parse(os.Args[1:])

	opts, err := flags.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sextant-dash: %v\n", err)
		os.Exit(1)
	}

	// A signal-bound context so Ctrl-C / SIGTERM winds the dash down (the alt-screen
	// program also quits on its own keys; this is the belt-and-braces escape).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dash.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "sextant-dash: %v\n", err)
		os.Exit(1)
	}
}

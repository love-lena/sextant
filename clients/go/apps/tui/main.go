// Command sextant-tui is the terminal UI: a first-class CLI/TUI feature that
// assembles the three master-detail browsers — clients, topics, artifacts —
// through the layout engine into a cockpit, holding one bus identity
// (ADR-0023/0024). It is its own client binary, just another client over the SDK
// with no special privilege (ADR-0014, ADR-0008).
//
// The browser dash (sextant-dash) is THE dash now (ADR-0046); sextant-tui is a
// peer terminal feature, NOT a deprecated or retired surface — it carries no
// serve/HTTP path, so it never serves anything. Launching is `sextant up` then
// `sextant-tui`: with no identity resolved and a local bus discoverable, it
// enrolls itself (named from $USER; --name overrides) and announces it in one
// line. An existing context is used as-is:
//
//	sextant-tui                              # first run self-enrolls; later runs reuse the context
//	sextant-tui --creds path/to.creds --store path/to/bus-store
//	sextant-tui --context my-context         # resolve creds + URL from a saved context
//
// Flags mirror the operator CLI's connection flags (--creds/--store/--url/
// --context, $SEXTANT_*) plus the terminal UI's own (--theme, --config, --name).
// Each browser is a list you step into: Enter opens the selected row's detail in
// the same pane (a DM, a topic conversation, a document reader); Esc pops one
// level back out. The layout keys (o/p, arrows, enter/esc, q) arrange the panes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/love-lena/sextant/clients/go/apps/internal/dash"
	"github.com/love-lena/sextant/clients/go/apps/internal/version"
)

func main() {
	fs := flag.NewFlagSet("sextant-tui", flag.ExitOnError)
	flags := dash.AddFlags(fs)
	ver := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(os.Args[1:])
	if *ver {
		fmt.Println("sextant-tui " + version.String())
		return
	}

	opts, err := flags.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sextant-tui: %v\n", err)
		os.Exit(1)
	}

	// A signal-bound context so Ctrl-C / SIGTERM winds the terminal UI down (the
	// alt-screen program also quits on its own keys; this is the belt-and-braces
	// escape).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dash.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "sextant-tui: %v\n", err)
		os.Exit(1)
	}
}

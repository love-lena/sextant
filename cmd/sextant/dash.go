package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/love-lena/sextant/internal/dash"
)

// cmdDash is the `sextant dash` alias: a thin subcommand that delegates to the
// shared dash.Run, so the alias and the standalone cmd/sextant-dash binary share
// one implementation (cleaner and more robust than exec-on-PATH; ADR-0023, 7.5).
// It parses the same flags as the binary (the connection flags plus the dash's
// own) and resolves the identity the same way every operation command does.
func cmdDash(args []string) {
	fs := flag.NewFlagSet("dash", flag.ExitOnError)
	flags := dash.AddFlags(fs)
	_ = fs.Parse(args)

	opts, err := flags.Resolve()
	if err != nil {
		fatal("%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dash.Run(ctx, opts); err != nil {
		fatal("%v", err)
	}
}

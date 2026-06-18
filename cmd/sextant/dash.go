package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/internal/dash"
)

// cmdDash is the `sextant dash` alias: a thin subcommand that delegates to the
// shared dash.Run, so the alias and the standalone cmd/sextant-dash binary share
// one implementation (cleaner and more robust than exec-on-PATH; ADR-0023, 7.5).
// It parses the same flags as the binary (the connection flags plus the dash's
// own) and resolves the identity the same way every operation command does.
//
// It also dispatches the `url` subcommand (`sextant dash url`), which reads the
// managed-dash state file and prints the URL — no bus connection required.
func cmdDash(args []string) {
	if len(args) > 0 && args[0] == "url" {
		cmdDashURL(args[1:])
		return
	}

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

// cmdDashURL implements `sextant dash url`: reads the managed-dash state file
// ($SEXTANT_HOME/dash.json) and prints the URL. If the file is absent it exits
// with a clear error — the dash is not running as a managed service.
func cmdDashURL(args []string) {
	fs := flag.NewFlagSet("dash url", flag.ExitOnError)
	_ = fs.Parse(args)

	stateFile := filepath.Join(clictx.Root(), "dash.json")
	state, err := dash.ReadStateFile(stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fatal("the dash is not running as a managed service — no state file at %s\n"+
				"  (start it with `sextant dash --serve --state-file %s`)", stateFile, stateFile)
		}
		fatal("read state file %s: %v", stateFile, err)
	}
	fmt.Println(state.URL)
}

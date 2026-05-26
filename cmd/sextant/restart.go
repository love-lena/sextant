package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// runRestart implements `sextant restart`. Stop then start, with clear
// transition prints so the operator sees both phases. Tolerates a
// not-running starting state — that just becomes "start fresh".
// Plan: plans/issues/feat-daemon-lifecycle-ergonomics.md (fix #3).
func runRestart(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant restart", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	stopTimeout := fs.Duration("stop-timeout", 30*time.Second, "max wait for graceful shutdown")
	startTimeout := fs.Duration("start-timeout", 30*time.Second, "max wait for runtime.json to reappear")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(restartUsage)
		return nil
	}

	cfg, err := loadDaemonConfig(*configDir, *dataDir)
	if err != nil {
		return err
	}
	return doRestart(os.Stdout, cfg, *stopTimeout, *startTimeout)
}

const restartUsage = `usage: sextant restart [--config-dir DIR] [--data-dir DIR]
                       [--stop-timeout 30s] [--start-timeout 30s]

Calls stop (SIGTERM + wait) then start (detached spawn + wait for
runtime.json). Each transition is announced on stdout so a wedged phase
is identifiable.`

// doRestart is the testable body of `sextant restart`. We pass the cfg
// loaded once at the top so a config-dir flag change in the middle
// can't confuse the two phases.
func doRestart(w io.Writer, cfg sextantd.Config, stopTimeout, startTimeout time.Duration) error {
	if err := doStop(w, cfg, stopTimeout); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stop phase: %w", err)
		}
	}
	printf(w, "starting…\n")
	return doStart(w, cfg, startTimeout)
}

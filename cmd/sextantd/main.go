// sextantd is the sextant supervisor daemon. M5 ships the skeleton:
// owns the signing CA, supervises NATS + ClickHouse, exposes a control
// Unix socket. Real RPC dispatch lands in M7; Docker spawning in M11.
//
// Plan: plans/bootstrap.md#M5
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/love-lena/sextant/pkg/sextantd"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextantd: %v", err)
	}
}

func run() error {
	// Subcommand dispatch: sextantd is otherwise flag-only, but `version`
	// must work without touching config / signal handling / log setup, so
	// it's routed here before any other startup happens. Keep this list
	// short — flag-based subcommands don't scale; if more arrive, migrate
	// sextantd to cobra in lockstep with the CLI.
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		return runVersion(os.Stdout)
	}

	fs := flag.NewFlagSet("sextantd", flag.ExitOnError)
	configPath := fs.String("config", "", "sextantd.toml path (default ~/.config/sextant/sextantd.toml)")
	testMode := fs.Bool("test-mode", false, "run in test mode (reserved for M17)")
	testID := fs.String("test-id", "", "test environment uuid (with --test-mode)")
	restart := fs.Bool("restart", false, "replace any already-running sextantd (graceful SIGTERM then start fresh)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *testMode {
		// M17 owns this branch; reject for now so we don't ship a
		// half-built mode under a real flag.
		return fmt.Errorf("--test-mode is reserved for M17 (test_id=%s)", *testID)
	}

	cfgPath := *configPath
	if cfgPath == "" {
		cfgDir, _, err := sextantd.DefaultPaths()
		if err != nil {
			return err
		}
		cfgPath = filepath.Join(cfgDir, "sextantd.toml")
	}

	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", cfgPath, err)
	}

	// Pre-startup probe: bail (or replace) if another sextantd already
	// owns runtime.json. Before this check, a duplicate start crashed on
	// the NATS/ClickHouse port-bind. See slug:feat-daemon-lifecycle-ergonomics
	// fix #4. This call may os.Exit; survivors
	// continue with normal startup. Runs before the log file is opened
	// so a benign double-start doesn't append a "starting…" line to a
	// log owned by the live daemon.
	checkExistingDaemonOrExit(cfg, *restart)

	// Open the daemon's own log sink BEFORE any other startup work so
	// the first "sextantd: starting…" line written by daemon.Start lands
	// in the file. Tees stderr + file via log.SetOutput; foreground
	// operators keep terminal feedback while doctor / post-mortem
	// debugging always has a canonical sink at <data_dir>/sextantd.log
	// (overridable via [log] file = "..." in sextantd.toml).
	logFile, err := openLogFile(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	// Signal handling: SIGTERM/SIGINT trigger the daemon's graceful
	// shutdown sequence (supervisor.Stop → signalProcessGroup → wait on
	// cmd.Wait with SIGKILL escalation). We deliberately do NOT cancel
	// the main ctx on signal — exec.CommandContext's default Cancel
	// callback sends SIGKILL only to the leader pid, which orphans
	// ClickHouse's watchdog child (the same leak vector 2903609 fixed
	// for the supervisor.Stop path). Instead we close shutdownCh, drive
	// d.Shutdown() to completion under its own ShutdownTimeout, and
	// only then cancel ctx as final cleanup.
	//
	// SIGHUP and SIGUSR2 are forwarded to the daemon for log-and-noop
	// handling (M5).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGUSR2)
	defer signal.Stop(sigs)

	d, err := newDaemon(cfg)
	if err != nil {
		return err
	}

	shutdownCh := make(chan struct{})
	var shutdownOnce sync.Once
	triggerShutdown := func() {
		shutdownOnce.Do(func() { close(shutdownCh) })
	}

	go func() {
		for {
			select {
			case sig := <-sigs:
				switch sig {
				case syscall.SIGTERM, syscall.SIGINT:
					log.Printf("sextantd: %s received, beginning graceful shutdown", sig)
					triggerShutdown()
					return
				case syscall.SIGHUP:
					log.Println("sextantd: SIGHUP received — re-read not yet implemented (M5)")
				case syscall.SIGUSR2:
					log.Println("sextantd: SIGUSR2 received — self-update handoff stub (M16)")
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if err := d.Start(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	// Block until either:
	//   - a signal arrived (shutdownCh closed)
	//   - a supervised unit fails terminally (d.Wait returns).
	//
	// Whichever fires first triggers Shutdown — and Shutdown completes
	// (driving the supervisor's signalProcessGroup → cmd.Wait → SIGKILL
	// escalation path) BEFORE we cancel ctx. Canceling ctx earlier would
	// let exec.CommandContext's default cancel callback SIGKILL only the
	// leader pid, orphaning ClickHouse's watchdog child.
	waitCh := make(chan error, 1)
	go func() { waitCh <- d.Wait() }()

	var runErr error
	select {
	case <-shutdownCh:
	case runErr = <-waitCh:
	}

	shutdownErr := d.Shutdown()
	cancel()
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("daemon exited: %w", runErr)
	}
	if shutdownErr != nil {
		return fmt.Errorf("shutdown: %w", shutdownErr)
	}
	return nil
}

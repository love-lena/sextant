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
	"syscall"

	"github.com/love-lena/sextant-initial/pkg/sextantd"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextantd: %v", err)
	}
}

func run() error {
	fs := flag.NewFlagSet("sextantd", flag.ExitOnError)
	configPath := fs.String("config", "", "sextantd.toml path (default ~/.config/sextant/sextantd.toml)")
	testMode := fs.Bool("test-mode", false, "run in test mode (reserved for M17)")
	testID := fs.String("test-id", "", "test environment uuid (with --test-mode)")
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

	// Signal handling: SIGTERM/SIGINT cancel the daemon ctx for graceful
	// shutdown; SIGHUP and SIGUSR2 are forwarded to the daemon for
	// log-and-noop handling (M5).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGUSR2)
	defer signal.Stop(sigs)

	d, err := newDaemon(cfg)
	if err != nil {
		return err
	}

	// Signal forwarder. SIGTERM/SIGINT call Shutdown; SIGHUP and SIGUSR2
	// just log (per specs/components/sextantd.md §"Signal handling").
	go func() {
		for {
			select {
			case sig := <-sigs:
				switch sig {
				case syscall.SIGTERM, syscall.SIGINT:
					log.Printf("sextantd: %s received, beginning graceful shutdown", sig)
					cancel()
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
	//   - ctx is canceled (signal handler called cancel)
	//   - a supervised unit fails terminally (d.Wait returns).
	//
	// Whichever fires first triggers Shutdown.
	waitCh := make(chan error, 1)
	go func() { waitCh <- d.Wait() }()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-waitCh:
	}

	shutdownErr := d.Shutdown()
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("daemon exited: %w", runErr)
	}
	if shutdownErr != nil {
		return fmt.Errorf("shutdown: %w", shutdownErr)
	}
	return nil
}

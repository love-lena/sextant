// sextant-shipper subscribes to NATS, decodes Envelopes, and writes
// them to ClickHouse with at-least-once delivery and a finite BoltDB
// spillover for ClickHouse-unreachable windows.
//
// Run as a separate process from sextantd (failure isolation): the
// operator (or a process supervisor like launchd/systemd) launches
// `sextant-shipper` alongside the daemon. Wire-up to sextantd's
// supervisor loop is deferred to M7+.
//
// Plan: plans/bootstrap.md#M6
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/love-lena/sextant-initial/pkg/shipper"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextant-shipper: %v", err)
	}
}

func run() error {
	fs := flag.NewFlagSet("sextant-shipper", flag.ContinueOnError)
	configPath := fs.String("config", "", "shipper.toml path (default ~/.config/sextant/shipper.toml)")
	runtimePath := fs.String("runtime-file", "", "runtime.json path (default ~/.local/share/sextant/runtime.json)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if *configPath == "" {
		p, err := shipper.DefaultConfigPath()
		if err != nil {
			return err
		}
		*configPath = p
	}
	if *runtimePath == "" {
		p, err := shipper.DefaultRuntimePath()
		if err != nil {
			return err
		}
		*runtimePath = p
	}

	cfg, err := shipper.ConfigFromFile(*configPath, *runtimePath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log.Printf("sextant-shipper: config=%s runtime=%s", *configPath, *runtimePath)
	log.Printf("sextant-shipper: nats=%s clickhouse=%s buffer=%s host=%s",
		cfg.NATS.URL, cfg.ClickHouse.Addr, cfg.Buffer.Dir, cfg.HostID())

	// Bound startup so a stuck dependency does not hang forever.
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()

	s, err := shipper.New(startupCtx, cfg)
	if err != nil {
		return fmt.Errorf("new shipper: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = closeCtx
		if cerr := s.Close(); cerr != nil {
			log.Printf("sextant-shipper: close: %v", cerr)
		}
	}()

	// Signal handling: SIGTERM/SIGINT graceful; SIGHUP log-and-noop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)

	go func() {
		for {
			select {
			case sig := <-sigs:
				switch sig {
				case syscall.SIGTERM, syscall.SIGINT:
					log.Printf("sextant-shipper: %s received; shutting down", sig)
					cancel()
					return
				case syscall.SIGHUP:
					log.Println("sextant-shipper: SIGHUP received — no-op")
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	runErr := s.Run(ctx)
	switch {
	case runErr == nil:
		return nil
	case errors.Is(runErr, shipper.ErrBackpressure):
		// Fail-closed exit per shipper spec: non-zero so an outer
		// supervisor can react.
		return fmt.Errorf("shipper backpressure: %w", runErr)
	default:
		return runErr
	}
}

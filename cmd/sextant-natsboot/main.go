// sextant-natsboot is a standalone harness around pkg/natsboot. It
// brings up a single-host nats-server instance with sextant's stream
// and KV layout and prints connection details. Used for ad-hoc testing
// during development and during M2 acceptance.
//
// Not part of the production daemon — sextantd (M5) drives the real
// production lifecycle via pkg/natsboot directly.
//
// Plan: plans/bootstrap.md#M2
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
	"time"

	"github.com/love-lena/sextant-initial/pkg/natsboot"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextant-natsboot: %v", err)
	}
}

func run() error {
	var (
		dataDir = flag.String("data-dir", "", "JetStream data dir (default ~/.local/share/sextant/nats)")
		host    = flag.String("host", "127.0.0.1", "bind host")
		port    = flag.Int("port", 0, "bind TCP port; 0 for OS-chosen")
		logFile = flag.String("log-file", "", "redirect nats-server output here (empty = discard)")
		verify  = flag.Bool("verify", false, "after bootstrap, re-verify every stream/bucket exists and exit")
		dur     = flag.Duration("run-for", 0, "if non-zero, stop after this duration instead of waiting for signal")
	)
	flag.Parse()

	if *dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
		*dataDir = filepath.Join(home, ".local", "share", "sextant", "nats")
	}

	cfg := natsboot.DefaultConfig(*dataDir)
	cfg.ListenHost = *host
	cfg.ListenPort = *port
	cfg.LogFile = *logFile

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := natsboot.Start(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if stopErr := srv.Stop(stopCtx); stopErr != nil && !errors.Is(stopErr, context.Canceled) {
			log.Printf("sextant-natsboot: stop: %v", stopErr)
		}
		stopCancel()
	}()

	fmt.Printf("nats-server listening on %s\n", srv.Address())
	fmt.Printf("operator user      %s\n", srv.OperatorUser())
	fmt.Printf("operator password  %s\n", srv.OperatorPassword())
	fmt.Printf("config             %s\n", srv.ConfigPath())
	fmt.Printf("data dir           %s\n", srv.DataDir())

	nc, err := srv.Connect()
	if err != nil {
		return fmt.Errorf("operator connect: %w", err)
	}
	defer nc.Close()

	bootCtx, bootCancel := context.WithTimeout(ctx, 30*time.Second)
	bootErr := natsboot.Bootstrap(bootCtx, nc, cfg.MaxBytesPerStream)
	bootCancel()
	if bootErr != nil {
		return fmt.Errorf("bootstrap: %w", bootErr)
	}
	fmt.Println("streams + kv buckets created")

	if *verify {
		vctx, vcancel := context.WithTimeout(ctx, 5*time.Second)
		vErr := natsboot.VerifyBootstrap(vctx, nc)
		vcancel()
		if vErr != nil {
			return fmt.Errorf("verify: %w", vErr)
		}
		fmt.Println("verify ok")
		return nil
	}

	if *dur > 0 {
		fmt.Printf("running for %s\n", *dur)
		select {
		case <-time.After(*dur):
		case <-ctxSignal(ctx):
		}
	} else {
		fmt.Println("press Ctrl+C to shut down")
		<-ctxSignal(ctx)
	}
	fmt.Println("shutting down")
	return nil
}

// ctxSignal returns a channel closed when ctx is canceled or SIGINT/SIGTERM is delivered.
func ctxSignal(ctx context.Context) <-chan struct{} {
	out := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer close(out)
		select {
		case <-ctx.Done():
		case <-sigs:
		}
	}()
	return out
}

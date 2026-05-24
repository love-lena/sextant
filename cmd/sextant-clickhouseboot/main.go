// sextant-clickhouseboot is a standalone harness around
// pkg/clickhouseboot. It boots clickhouse-server, applies the sextant
// schema migrations, and prints connection details. Used for ad-hoc
// testing and as an M3 acceptance helper.
//
// Plan: plans/bootstrap.md#M3
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

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/clickhouseboot"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sextant-clickhouseboot: %v", err)
	}
}

func run() error {
	var (
		dataDir = flag.String("data-dir", "", "ClickHouse data dir (default ~/.local/share/sextant/clickhouse)")
		host    = flag.String("host", "127.0.0.1", "bind host")
		http    = flag.Int("http-port", 0, "HTTP port; 0 = OS-picked")
		tcp     = flag.Int("tcp-port", 0, "native TCP port; 0 = OS-picked")
		logFile = flag.String("log-file", "", "redirect clickhouse-server output (empty = discard)")
		verify  = flag.Bool("verify", false, "after migration, run a roundtrip insert/query and exit")
		dur     = flag.Duration("run-for", 0, "if non-zero, stop after this duration instead of waiting for signal")
	)
	flag.Parse()

	if *dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
		*dataDir = filepath.Join(home, ".local", "share", "sextant", "clickhouse")
	}

	cfg := clickhouseboot.DefaultConfig(*dataDir)
	cfg.ListenHost = *host
	cfg.HTTPPort = *http
	cfg.TCPPort = *tcp
	cfg.LogFile = *logFile

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := clickhouseboot.Start(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if stopErr := srv.Stop(stopCtx); stopErr != nil && !errors.Is(stopErr, context.Canceled) {
			log.Printf("sextant-clickhouseboot: stop: %v", stopErr)
		}
		stopCancel()
	}()

	fmt.Printf("clickhouse listening (tcp %s, http %s)\n", srv.TCPAddress(), srv.HTTPAddress())
	fmt.Printf("database  %s\n", srv.Database())
	fmt.Printf("user      %s\n", srv.User())
	fmt.Printf("password  %s\n", srv.Password())
	fmt.Printf("config    %s\n", srv.ConfigPath())
	fmt.Printf("data dir  %s\n", *dataDir)

	conn, err := srv.Open(ctx)
	if err != nil {
		return fmt.Errorf("open conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := clickhouseboot.Apply(ctx, conn); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	fmt.Println("migrations applied")

	if *verify {
		if err := smokeCheck(ctx, conn); err != nil {
			return fmt.Errorf("verify: %w", err)
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

// smokeCheck inserts a row into the events table and queries it back.
// Catches schema regressions and shipper-prerequisite breakages.
func smokeCheck(ctx context.Context, conn driver.Conn) error {
	id := uuid.New()
	traceID := uuid.New()
	spanID := uuid.New()
	now := time.Now().UTC()

	if err := conn.Exec(ctx,
		`INSERT INTO events (id, ts, subject, from_kind, from_id, to_kind, to_id,
			trace_id, span_id, parent_span_id, kind, proto_version, payload, idempotency_key, reply_to)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, now, "agents.smoke.lifecycle", "daemon", "daemon-host", "ui", "",
		traceID, spanID, uuid.Nil, "lifecycle", "1.0",
		`{"transition":"started"}`, "", ""); err != nil {
		return fmt.Errorf("insert: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT id, subject FROM events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return fmt.Errorf("no row found for id %s", id)
	}
	var (
		gotID   uuid.UUID
		gotSubj string
	)
	if err := rows.Scan(&gotID, &gotSubj); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	if gotID != id {
		return fmt.Errorf("id mismatch: got %s want %s", gotID, id)
	}
	if gotSubj != "agents.smoke.lifecycle" {
		return fmt.Errorf("subject mismatch: got %q", gotSubj)
	}
	return nil
}

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

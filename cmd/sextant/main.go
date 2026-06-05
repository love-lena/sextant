// Command sextant is the operator CLI: run the embedded bus and mint per-client
// credentials.
//
// (A full resource-verb CLI — likely Cobra — comes later; v1 dispatches a
// couple of subcommands with the standard library.)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/conninfo"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "up":
		cmdUp(os.Args[2:])
	case "token":
		cmdToken(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "sextant: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `sextant — a protocol + SDK for AI agents over a bus

usage:
  sextant up    [--store DIR] [--port N]        run the embedded bus
  sextant token <client-id> [--store DIR] [--out FILE]
                                                mint a client credentials file

`)
}

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "JetStream + key-material directory")
	port := fs.Int("port", 0, "listen port (0 = random)")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*store, 0o700); err != nil { // holds key material + JS data
		fatal("create store dir: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := bus.Start(ctx, bus.Config{StoreDir: *store, Port: *port})
	if err != nil {
		fatal("%v", err)
	}

	infoPath := filepath.Join(*store, conninfo.DefaultFile)
	if err := conninfo.Write(infoPath, conninfo.Info{URL: b.ClientURL()}); err != nil {
		b.Shutdown()
		fatal("write discovery file: %v", err)
	}

	fmt.Printf("sextant bus up\n  url:        %s\n  discovery:  %s\n\n"+
		"give each client its own identity:\n  sextant token <client-id> --store %s\n\n"+
		"Ctrl-C to drain and stop.\n",
		b.ClientURL(), infoPath, *store)

	<-ctx.Done()
	stop() // restore default signal handling; a second signal force-quits

	fmt.Println("\ndraining…")
	if err := b.Drain(); err != nil {
		fmt.Fprintf(os.Stderr, "drain: %v\n", err)
	}
	time.Sleep(200 * time.Millisecond) // brief grace for delivery
	b.Shutdown()
	fmt.Println("bus down")
}

func cmdToken(args []string) {
	// The display_name is the first positional; flags follow it (Go's flag package
	// stops at the first non-flag, so the name can't come after the flags).
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant token <display-name> [--store DIR] [--out FILE]")
	}
	displayName := args[0]

	fs := flag.NewFlagSet("token", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "JetStream + key-material directory")
	out := fs.String("out", "", "write the creds file here (default: <store>/tokens/<id>.creds; '-' for stdout)")
	_ = fs.Parse(args[1:])

	// The bus mints the client's primary id (a ULID); display_name is the
	// human label carried in the credential.
	creds, id, err := bus.MintClientToken(*store, displayName)
	if err != nil {
		fatal("%v", err)
	}

	if *out == "-" {
		fmt.Print(creds)
		return
	}
	path := *out
	if path == "" {
		dir := filepath.Join(*store, "tokens")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fatal("create tokens dir: %v", err)
		}
		path = filepath.Join(dir, id+".creds")
	}
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		fatal("write creds: %v", err)
	}
	fmt.Printf("minted credentials for %q (id %s):\n  %s\n", displayName, id, path)
}

// defaultStore is a stable, CWD-independent location so `up` and `token` run
// from different directories still share the same key material.
func defaultStore() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant", "jetstream")
	}
	return filepath.Join(".sextant", "jetstream")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sextant: "+format+"\n", args...)
	os.Exit(1)
}

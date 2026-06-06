// Command sextant is the operator CLI: run the embedded bus, issue and retire
// client identities, and drive the protocol operations.
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
	case "publish":
		cmdPublish(os.Args[2:])
	case "subscribe":
		cmdSubscribe(os.Args[2:])
	case "read":
		cmdRead(os.Args[2:])
	case "clients":
		cmdClients(os.Args[2:])
	case "artifact":
		cmdArtifact(os.Args[2:])
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

identities (the bus is the sole minter; keys never leave it — ADR-0020):
  sextant clients register <name> [--kind K]    operator mints for another
  sextant clients register --self  [--kind K]   bootstrap/enrollment: mint for self
  sextant clients retire   <id>                 decommission an identity (operator)
  sextant clients list     [--json]             the directory (online + offline)

operations (each needs --creds; bus URL from --store discovery or --url):
  sextant publish   <subject> <record-json>
  sextant read      <subject> [--since N] [--limit N] [--json]
  sextant subscribe <subject> [--all] [--json]
  sextant artifact  create|update|get|delete|watch <name> [<record-json>] [--rev N] [--json]

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

	fmt.Printf("sextant bus up\n  url:        %s\n  discovery:  %s\n  operator:   %s\n\n"+
		"issue a client identity (the bus mints it; keys stay in the bus):\n"+
		"  sextant clients register <name> --store %s\n\n"+
		"Ctrl-C to drain and stop.\n",
		b.ClientURL(), infoPath, bus.OperatorCredsPath(*store), *store)

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

// defaultStore is a stable, CWD-independent location so `up` and the client
// commands run from different directories still share the same key material.
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

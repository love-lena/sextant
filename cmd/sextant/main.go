// Command sextant is the operator CLI. For now it runs the embedded bus.
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
  sextant up [--store DIR] [--port N]   run the embedded bus

`)
}

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "JetStream storage directory")
	port := fs.Int("port", 0, "listen port (0 = random)")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*store, 0o755); err != nil {
		fatal("create store dir: %v", err)
	}

	b, err := bus.Start(context.Background(), bus.Config{StoreDir: *store, Port: *port})
	if err != nil {
		fatal("%v", err)
	}

	infoPath := filepath.Join(*store, conninfo.DefaultFile)
	info := conninfo.Info{
		URL:            b.ClientURL(),
		ClientUser:     b.ClientUser(),
		ClientPassword: b.ClientPassword(),
	}
	if err := conninfo.Write(infoPath, info); err != nil {
		b.Shutdown()
		fatal("write conn info: %v", err)
	}

	fmt.Printf("sextant bus up\n  url:        %s\n  client:     %s\n  conn info:  %s\n\nCtrl-C to drain and stop.\n",
		b.ClientURL(), b.ClientUser(), infoPath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	fmt.Println("\ndraining…")
	if err := b.Drain(); err != nil {
		fmt.Fprintf(os.Stderr, "drain: %v\n", err)
	}
	time.Sleep(200 * time.Millisecond) // brief grace for delivery
	b.Shutdown()
	fmt.Println("bus down")
}

func defaultStore() string {
	return filepath.Join(".sextant", "jetstream")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sextant: "+format+"\n", args...)
	os.Exit(1)
}

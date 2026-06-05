package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

// The operation subcommands — the operator/test face of the protocol's
// operations (TASK-28). Command names have exact parity with the operations in
// protocol/methods.json (no aliases): publish, subscribe, read, clients list,
// artifact create|update|get|delete|watch. Each is a thin wrapper that connects
// an SDK client and invokes one operation, so the CLI and the SDK share one
// surface (the conformance test pins the parity).

// cliOperations maps each protocol operation (protocol/methods.json) to its CLI
// command. It is the source of truth the conformance test checks both ways:
// every operation has exactly one command, and the CLI invents no command that
// isn't an operation — making "one surface, many faces" mechanical, not
// disciplinary (TASK-28). The MCP server (TASK-22) extends the same test with
// its tool table.
var cliOperations = map[string]string{
	"message.publish":   "publish",
	"message.read":      "read",
	"message.subscribe": "subscribe",
	"artifact.create":   "artifact create",
	"artifact.update":   "artifact update",
	"artifact.get":      "artifact get",
	"artifact.delete":   "artifact delete",
	"artifact.watch":    "artifact watch",
	"clients.list":      "clients list",
}

// connFlags are the bus-connection flags shared by every operation command.
type connFlags struct {
	creds *string
	store *string
	url   *string
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		creds: fs.String("creds", "", "client credentials file (mint with `sextant token`)"),
		store: fs.String("store", defaultStore(), "JetStream + key-material directory (for bus discovery)"),
		url:   fs.String("url", "", "bus URL (default: discovery file under --store)"),
	}
}

// connect dials an SDK client from the connection flags. ctx governs the call.
func (cf connFlags) connect(ctx context.Context) *sextant.Client {
	if *cf.creds == "" {
		fatal("--creds is required (mint one with `sextant token <display-name>`)")
	}
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    *cf.creds,
		URL:          *cf.url,
		ConnInfoPath: filepath.Join(*cf.store, conninfo.DefaultFile),
		Kind:         "cli",
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		fatal("connect: %v", err)
	}
	return c
}

// signalCtx is a context cancelled on Ctrl-C / SIGTERM, for streaming commands.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func cmdPublish(args []string) {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant publish <subject> <record-json> [--creds F] [--store DIR] [--url U]")
	}
	subject, record := args[0], args[1]
	if !json.Valid([]byte(record)) {
		fatal("record must be valid JSON")
	}
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	cf := addConnFlags(fs)
	_ = fs.Parse(args[2:])

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	if err := c.Publish(ctx, subject, json.RawMessage(record)); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("published to %s\n", subject)
}

func cmdRead(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant read <subject> [--since N] [--limit N] [--json] [--creds F] [--store DIR]")
	}
	subject := args[0]
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	since := fs.Uint64("since", 0, "cursor: next sequence to read (0 = from the start)")
	limit := fs.Int("limit", 100, "max messages to return")
	asJSON := fs.Bool("json", false, "emit each frame as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args[1:])

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	frames, next, err := c.FetchMessages(ctx, subject, *since, *limit)
	if err != nil {
		fatal("%v", err)
	}
	for _, f := range frames {
		printFrame(subject, f, *asJSON)
	}
	fmt.Fprintf(os.Stderr, "(%d messages; next cursor %d)\n", len(frames), next)
}

func cmdSubscribe(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant subscribe <subject> [--all] [--json] [--creds F] [--store DIR]")
	}
	subject := args[0]
	fs := flag.NewFlagSet("subscribe", flag.ExitOnError)
	all := fs.Bool("all", false, "replay retained history before live messages")
	asJSON := fs.Bool("json", false, "emit each frame as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args[1:])

	ctx, stop := signalCtx()
	defer stop()
	c := cf.connect(ctx)
	defer c.Close()

	var opts []sextant.SubOption
	if *all {
		opts = append(opts, sextant.DeliverAll())
	}
	sub, err := c.Subscribe(ctx, subject, func(m sextant.Message) {
		printFrame(m.Subject, m.Frame, *asJSON)
	}, opts...)
	if err != nil {
		fatal("%v", err)
	}
	defer sub.Stop()
	fmt.Fprintf(os.Stderr, "subscribed to %s (Ctrl-C to stop)\n", subject)
	<-ctx.Done()
}

func cmdClients(args []string) {
	// Exact parity with the operation name: `clients list`.
	if len(args) < 1 || args[0] != "list" {
		fatal("usage: sextant clients list [--json] [--creds F] [--store DIR]")
	}
	fs := flag.NewFlagSet("clients list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the directory as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args[1:])

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	clients, err := c.ListClients(ctx)
	if err != nil {
		fatal("%v", err)
	}
	if *asJSON {
		emitJSON(clients)
		return
	}
	for _, ci := range clients {
		fmt.Printf("%s  %-20s  %s  epoch=%d\n", ci.ID, ci.DisplayName, ci.Kind, ci.Epoch)
	}
	fmt.Fprintf(os.Stderr, "(%d clients)\n", len(clients))
}

func cmdArtifact(args []string) {
	if len(args) < 1 {
		fatal("usage: sextant artifact create|update|get|delete|watch <name> [...]")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "create":
		artifactWrite(rest, false)
	case "update":
		artifactWrite(rest, true)
	case "get":
		artifactGet(rest)
	case "delete":
		artifactDelete(rest)
	case "watch":
		artifactWatch(rest)
	default:
		fatal("unknown artifact verb %q (create|update|get|delete|watch)", verb)
	}
}

func artifactWrite(args []string, update bool) {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant artifact create|update <name> <record-json> [--rev N] [--creds F]")
	}
	name, record := args[0], args[1]
	if !json.Valid([]byte(record)) {
		fatal("record must be valid JSON")
	}
	fs := flag.NewFlagSet("artifact", flag.ExitOnError)
	rev := fs.Uint64("rev", 0, "expected current revision (update only; compare-and-set)")
	cf := addConnFlags(fs)
	_ = fs.Parse(args[2:])

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	var (
		newRev uint64
		err    error
	)
	if update {
		newRev, err = c.UpdateArtifact(ctx, name, json.RawMessage(record), *rev)
	} else {
		newRev, err = c.CreateArtifact(ctx, name, json.RawMessage(record))
	}
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("%s now at revision %d\n", name, newRev)
}

func artifactGet(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant artifact get <name> [--json] [--creds F]")
	}
	name := args[0]
	fs := flag.NewFlagSet("artifact get", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the artifact as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args[1:])

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	a, err := c.GetArtifact(ctx, name)
	if err != nil {
		fatal("%v", err)
	}
	if *asJSON {
		emitJSON(a)
		return
	}
	fmt.Printf("%s (revision %d)\n%s\n", a.Name, a.Revision, a.Record)
}

func artifactDelete(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant artifact delete <name> [--creds F]")
	}
	name := args[0]
	fs := flag.NewFlagSet("artifact delete", flag.ExitOnError)
	cf := addConnFlags(fs)
	_ = fs.Parse(args[1:])

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	if err := c.DeleteArtifact(ctx, name); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("deleted %s\n", name)
}

func artifactWatch(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant artifact watch <name> [--json] [--creds F]")
	}
	name := args[0]
	fs := flag.NewFlagSet("artifact watch", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit each change as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args[1:])

	ctx, stop := signalCtx()
	defer stop()
	c := cf.connect(ctx)
	defer c.Close()
	w, err := c.WatchArtifact(ctx, name, func(ch sextant.ArtifactChange) {
		if *asJSON {
			emitJSON(ch)
			return
		}
		if ch.Deleted {
			fmt.Printf("%s deleted\n", name)
			return
		}
		fmt.Printf("%s (revision %d)\n%s\n", name, ch.Revision, ch.Record)
	})
	if err != nil {
		fatal("%v", err)
	}
	defer func() { _ = w.Stop() }()
	fmt.Fprintf(os.Stderr, "watching %s (Ctrl-C to stop)\n", name)
	<-ctx.Done()
}

// printFrame renders one message frame: JSON when asJSON, else a compact line.
func printFrame(subject string, f wire.Frame, asJSON bool) {
	if asJSON {
		emitJSON(f)
		return
	}
	fmt.Printf("[%s] %s <%s> %s\n", subject, f.ID, f.Author, f.Record)
}

func emitJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal("encode json: %v", err)
	}
	fmt.Println(string(b))
}

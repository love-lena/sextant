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
	"time"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/internal/selfenroll"
	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

// The operation subcommands — the operator/test face of the protocol's
// operations (TASK-28). Command names have exact parity with the operations in
// protocol/methods.json (no aliases): publish, subscribe, read, clients list,
// artifact create|update|get|list|delete|watch. Each is a thin wrapper that connects
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
	"artifact.list":     "artifact list",
	"artifact.delete":   "artifact delete",
	"artifact.watch":    "artifact watch",
	"clients.list":      "clients list",
	"clients.register":  "clients register",
	"clients.retire":    "clients retire",
}

// connFlags are the bus-connection flags shared by every operation command.
type connFlags struct {
	creds   *string
	store   *string
	url     *string
	context *string
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		creds:   fs.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (issue with `sextant clients register`; or set $SEXTANT_CREDS)"),
		store:   fs.String("store", defaultStore(), "JetStream + key-material directory for bus discovery (or set $SEXTANT_STORE)"),
		url:     fs.String("url", "", "bus URL (default: discovery file under --store)"),
		context: fs.String("context", os.Getenv("SEXTANT_CONTEXT"), "saved context to connect as (default: the active one; see `sextant context`)"),
	}
}

// resolve picks the credentials and bus URL for a connection — the precedence
// lives in clictx.Resolve, shared with sextant-mcp.
func (cf connFlags) resolve() (creds, url string, err error) {
	rc, err := clictx.Resolve(*cf.creds, *cf.url, *cf.context)
	if err != nil {
		return "", "", err
	}
	return rc.Creds, rc.URL, nil
}

// connect dials an SDK client from the connection flags. ctx governs the call.
func (cf connFlags) connect(ctx context.Context) *sextant.Client {
	creds, url, err := cf.resolve()
	if err != nil {
		fatal("%v", err)
	}
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    creds,
		URL:          url,
		ConnInfoPath: filepath.Join(*cf.store, conninfo.DefaultFile),
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
	if len(args) < 1 {
		fatal("usage: sextant clients register|retire|list ...")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		clientsList(rest)
	case "register":
		clientsRegister(rest)
	case "retire":
		clientsRetire(rest)
	default:
		fatal("unknown clients verb %q (register|retire|list)", verb)
	}
}

// clientsList prints the directory: every issued identity, online and offline
// (ADR-0020), with its presence in the last column. Offline clients are shown by
// default — that durable directory is the point.
func clientsList(args []string) {
	fs := flag.NewFlagSet("clients list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the directory as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args)

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
		presence := "offline"
		if ci.Online {
			presence = "online"
		}
		fmt.Printf("%s  %-20s  %-10s  epoch=%d  %s\n", ci.ID, ci.DisplayName, ci.Kind, ci.Epoch, presence)
	}
	fmt.Fprintf(os.Stderr, "(%d clients)\n", len(clients))
}

// safeCredsName picks a filesystem-safe basename for a client's default creds
// file. A display name may legitimately contain a path separator (the bus allows
// names like "a/b"), which would make `<store>/<name>.creds` fail on a missing
// subdir or escape the store ("../x"). In that case fall back to the bus-minted
// id — always a flat, safe token — so a successful mint never strands its creds.
func safeCredsName(name, id string) string {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return id
	}
	return name
}

// clientsRegister is the issuance command (ADR-0020). Two auth modes, one op:
//   - held-identity (default): the operator mints for another — `register <name>`
//     — connecting with the operator credential `sextant up` provisioned.
//   - bootstrap/enrollment (--self): an identity-less local process mints for
//     itself — `register --self` — connecting with the enrollment credential.
//
// The bus mints the identity and returns its credential; the CLI writes it to a
// file and prints the path. The CLI never touches the signing keys.
func clientsRegister(args []string) {
	// A held-mode name may be the first positional (flags follow it; Go's flag
	// package stops at the first non-flag). --self takes no positional.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("clients register", flag.ExitOnError)
	self := fs.Bool("self", false, "bootstrap/enrollment: mint an identity for this local process")
	kind := fs.String("kind", "client", "what the new client is (e.g. worker, reviewer)")
	nameFlag := fs.String("name", "", "display name (for --self, defaults to $USER)")
	store := fs.String("store", defaultStore(), "bus store dir: discovery + issuer credentials (or set $SEXTANT_STORE)")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	out := fs.String("out", "", "held mode: write the new creds here (default: <store>/<name>.creds)")
	force := fs.Bool("force", false, "with --self: re-enroll, replacing an existing context of the same name")
	noPrincipal := fs.Bool("no-principal", false, "with --self: do not claim the bus principal, even if it is still unclaimed (ADR-0031)")
	_ = fs.Parse(args)
	if *nameFlag != "" {
		name = *nameFlag
	}

	ctx := context.Background()

	// Self-enrollment is "I am now this identity": the shared selfenroll package
	// pre-flights (before the bus mints anything, so a bad request — a name that
	// can't be a context handle, --out, or an un-forced clobber — never strands a
	// fresh identity), mints over the enrollment credential, saves the identity as
	// a local context (creds in the context store, separate from the bus --store),
	// and makes it active, so subsequent commands need no connection flags
	// (ADR-0021). The dash's zero-config first run uses the same implementation.
	if *self {
		if name == "" {
			name = selfenroll.SelfName()
		}
		if err := selfenroll.Check(name, *out, *force); err != nil {
			fatal("%v", err)
		}
		if selfenroll.ResolveBusURL(*url, *store) == "" {
			fmt.Fprintln(os.Stderr, "warning: no bus URL recorded (no --url and no discovery file under --store)")
		}
		res, err := selfenroll.Enroll(ctx, name, *kind, *url, *store, *force, !*noPrincipal)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("enrolled as %s\n  creds:   %s\n  context: %s (now active)\n", res.ID, res.CredsPath, res.Name)
		if res.ClaimedPrincipal {
			fmt.Printf("  principal: this seat is now the bus principal\n")
		}
		return
	}

	// Held-identity mode mints creds to hand to someone else, so it just writes
	// the creds file (to --out or the store) and creates no context.
	if name == "" {
		fatal("register needs a <name> (or use --self to enroll this process)")
	}
	id, path, err := registerHeld(ctx, *store, *url, name, *kind, *out)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("registered %s as %s\n  creds: %s\n", name, id, path)
}

// registerHeld is the reusable held-identity mint-to-file core (ADR-0020): it
// connects with the operator credential under store, mints a NEW identity named
// name with kind, and writes the returned creds (0600) to outPath — or, when
// outPath is empty, to <store>/<safe-name>.creds. It returns the minted id and
// the creds path. It creates no context; the caller decides whether to record
// one (the CLI does not; `sextant components` writes its own under
// $SEXTANT_HOME, so the operator's active context is never disturbed). The CLI's
// held-mode register and the components first-run mint share this one path.
func registerHeld(ctx context.Context, store, url, name, kind, outPath string) (id, credsPath string, err error) {
	iss, err := sextant.ConnectIssuer(ctx, sextant.Options{
		CredsPath:    bus.OperatorCredsPath(store),
		URL:          url,
		ConnInfoPath: filepath.Join(store, conninfo.DefaultFile),
	})
	if err != nil {
		return "", "", fmt.Errorf("connect: %w", err)
	}
	defer iss.Close()
	res, err := iss.Register(ctx, name, kind)
	if err != nil {
		return "", "", err
	}
	path := outPath
	if path == "" {
		path = filepath.Join(store, safeCredsName(name, res.ID)+".creds")
	}
	if err := os.WriteFile(path, []byte(res.Creds), 0o600); err != nil {
		return res.ID, "", fmt.Errorf("write creds: %w", err)
	}
	return res.ID, path, nil
}

// clientsRetire decommissions an identity for good (operator-only). It connects
// with the operator credential and asks the bus to delete the identity.
func clientsRetire(args []string) {
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("clients retire", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir: discovery + operator credential (or set $SEXTANT_STORE)")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	_ = fs.Parse(args)
	if id == "" {
		fatal("usage: sextant clients retire <id> [--store DIR] [--url U]")
	}

	ctx := context.Background()
	iss, err := sextant.ConnectIssuer(ctx, sextant.Options{
		CredsPath:    bus.OperatorCredsPath(*store),
		URL:          *url,
		ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
	})
	if err != nil {
		fatal("connect: %v", err)
	}
	defer iss.Close()
	if err := iss.Retire(ctx, id); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("retired %s\n", id)
}

func cmdArtifact(args []string) {
	if len(args) < 1 {
		fatal("usage: sextant artifact create|update|get|list|delete|watch <name> [...]")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "create":
		artifactWrite(rest, false)
	case "update":
		artifactWrite(rest, true)
	case "get":
		artifactGet(rest)
	case "list":
		artifactList(rest)
	case "delete":
		artifactDelete(rest)
	case "watch":
		artifactWatch(rest)
	default:
		fatal("unknown artifact verb %q (create|update|get|list|delete|watch)", verb)
	}
}

// artifactList prints the artifacts directory: every artifact's name, current
// revision, and update time (ADR-0024 discovery). It carries no records — a
// client lists, then `artifact get`s the one it wants.
func artifactList(args []string) {
	fs := flag.NewFlagSet("artifact list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the directory as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args)

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	arts, err := c.ListArtifacts(ctx)
	if err != nil {
		fatal("%v", err)
	}
	if *asJSON {
		emitJSON(arts)
		return
	}
	for _, a := range arts {
		fmt.Printf("%-30s  rev=%-4d  %s\n", a.Name, a.Revision, a.Updated.Format(time.RFC3339))
	}
	fmt.Fprintf(os.Stderr, "(%d artifacts)\n", len(arts))
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

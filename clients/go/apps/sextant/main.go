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

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/bus/buscfg"
	"github.com/love-lena/sextant/clients/go/apps/internal/version"
	"github.com/love-lena/sextant/protocol/conninfo"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "up":
		cmdUp(os.Args[2:])
	case "doctor":
		cmdDoctor(os.Args[2:])
	case "publish":
		cmdPublish(os.Args[2:])
	case "subscribe":
		cmdSubscribe(os.Args[2:])
	case "read":
		cmdRead(os.Args[2:])
	case "clients":
		cmdClients(os.Args[2:])
	case "principal":
		cmdPrincipal(os.Args[2:])
	case "context":
		cmdContext(os.Args[2:])
	case "artifact":
		cmdArtifact(os.Args[2:])
	case "dash":
		cmdDash(os.Args[2:])
	case "lamp":
		cmdLamp(os.Args[2:])
	case "workflow":
		cmdWorkflow(os.Args[2:])
	case "config":
		cmdConfig(os.Args[2:])
	case "components":
		cmdComponents(os.Args[2:])
	case "secret":
		cmdSecret(os.Args[2:])
	case "update":
		cmdUpdate(os.Args[2:])
	case "version", "--version":
		fmt.Println("sextant " + version.String())
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

leaf-node federation (a remote box links a local bus to the hub — ADR-0038; default off):
  sextant up --leaf-listen <host:port>          hub: accept remote leaf links (behind a secure transport)
  sextant up --leaf-remote nats-leaf://hub:PORT --leaf-bundle B --leaf-creds C
                                                run as a leaf (JetStream stays at the hub)

browser dash (a co-equal TS client over ws — ADR-0044; default off):
  sextant up --ws-listen <host:port>            open a loopback WebSocket listener for the browser dash

bus config (settings 'up' reads on startup — the brew-services path; flag > env > config):
  sextant config set leaf-listen <host:port>    enable the leaf listener via the config file
  sextant config set ws-listen <host:port>      enable the bus WebSocket listener via the config file
  sextant config set port <n>                   pin a deterministic listen port (survives brew restart; 0 clears)
  sextant config get [leaf-listen|ws-listen|port]   show the current config

health / observability (read-only — diagnose a bus that won't come up):
  sextant doctor    [--store DIR]               report bus reachability, port, leaf state, launchd job

identities (the bus is the sole minter; keys never leave it — ADR-0020):
  sextant clients register <name> [--kind K]    operator mints for another
  sextant clients register --self  [--kind K]   bootstrap/enrollment: mint for self
  sextant clients retire   <id>                 decommission an identity (operator)
  sextant clients list     [--json]             the directory (online + offline)

the principal (the one human's client whose messages are operator-equivalent — ADR-0030):
  sextant principal set <ulid>                  re-point the principal (operator-only)
  sextant principal get                         read the current principal (any client)

contexts (saved URL+identity+creds, so operations need no flags — ADR-0021):
  sextant context add <name> --creds F          save a context (and activate it)
  sextant context use <name>                    make <name> the active context
  sextant context list                          list saved contexts
  sextant context current                       print the active context name

operations (creds from --creds, $SEXTANT_CREDS, or the active context):
  sextant publish   <subject> <record-json>
  sextant read      <subject> [--since N] [--limit N] [--json]
  sextant subscribe <subject> [--all] [--json]
  sextant artifact  create|update|get|list|delete|watch [<name>] [<record-json>] [--rev N] [--json]

the dash (a cockpit of three master-detail browsers over the same SDK — ADR-0024):
  sextant dash      [--theme light|dark|auto] [--config F] [--name N]
                    (alias for the sextant-dash binary; same connection flags;
                    first run with no identity self-enrolls against a local bus)

ambient warmth (a small lamp artifact — first run places one; toggles thereafter):
  sextant lamp      [show]                      toggle the lamp on/off (show just prints state)

agentic dev workflow (run a workflow-def artifact: plan→review→gate→PR — TASK-98):
  sextant workflow run <name> [--dry-run]       read the named workflow-def artifact + launch the orchestrator

managed runtimes (the agent runtimes as keep-alive, OS-managed services — macOS):
  sextant components status [name]              installed? loaded? running? (all if no name)
  sextant components start   [name | --all]     write the LaunchAgent + kickstart + health-check
  sextant components stop    [name | --all]     bootout the service (the plist stays on disk)
  sextant components restart [name | --all]     stop then start
  (managed: dispatcher, workflow, violet — the bus stays the Homebrew service)

secrets (violet's Anthropic key, stored 0600 — never in a launchd plist):
  sextant secret set anthropic                  prompt (no echo) + write violet.env; restart violet if running

staying current (Homebrew installs — see the README for taps + the plugin):
  sextant update                                brew update && brew upgrade the tap formula

version:
  sextant version                               print the build (release tag or dev + commit)

environment (avoids repeating the flags):
  SEXTANT_STORE   default for --store (the bus store dir; discovery + creds)
  SEXTANT_CREDS   default for --creds (the client credentials file)
  SEXTANT_CONTEXT default for --context (the saved context to connect as)
  SEXTANT_HOME    where contexts live (default: <user-config>/sextant)
  SEXTANT_LEAF_LISTEN  leaf-listen for 'up' when no --leaf-listen flag (overrides the config file)
  SEXTANT_WS_LISTEN    ws-listen for 'up' when no --ws-listen flag (overrides the config file)

`)
}

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "JetStream + key-material directory (or set $SEXTANT_STORE)")
	port := fs.Int("port", 0, "listen port (0 = random)")
	// Leaf-node federation (ADR-0038), all default-off (no behavior change unset).
	leafListen := fs.String("leaf-listen", "", "open a leaf listener at host:port so a remote leaf can link in (hub mode; behind a secure transport)")
	// Bus WebSocket listener (ADR-0044), default-off: lets a browser dash connect
	// as a co-equal TS client over ws. Loopback-only, behind a secure transport.
	wsListen := fs.String("ws-listen", "", "open a loopback WebSocket listener at host:port so a browser dash can connect as a co-equal TS client (ADR-0044)")
	leafRemote := fs.String("leaf-remote", "", "run as a LEAF linking to this hub's nats-leaf:// URL (JetStream stays at the hub)")
	leafCreds := fs.String("leaf-creds", "", "leaf mode: the hub-minted link credential file (with --leaf-remote)")
	leafBundle := fs.String("leaf-bundle", "", "leaf mode: the hub's public trust bundle file (with --leaf-remote)")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*store, 0o700); err != nil { // holds key material + JS data
		fatal("create store dir: %v", err)
	}

	// Resolve the hub leaf-listen address: flag > $SEXTANT_LEAF_LISTEN > config
	// file > OFF. The flag wins (explicit / dev); the env var is a convenience;
	// the config file is the brew-services path (launchd drops env + the plist on
	// restart, so a file the bus reads is what survives `brew services restart`).
	// A malformed/unreadable config fails loud here — never a silent default-off.
	cfg, err := buscfg.Load(buscfg.Path(*store))
	if err != nil {
		fatal("%v", err)
	}
	*leafListen = resolveLeafListen(fs, *leafListen, os.Getenv("SEXTANT_LEAF_LISTEN"), cfg.LeafListen)
	// WebSocket-listen precedence mirrors leaf-listen (ADR-0044): flag (when
	// explicitly passed) > $SEXTANT_WS_LISTEN > config-file value > "" (off). The
	// config-file value is the brew-services path the dash relies on.
	*wsListen = resolveWebSocketListen(fs, *wsListen, os.Getenv("SEXTANT_WS_LISTEN"), cfg.WebSocketListen)
	// Port precedence mirrors leaf-listen: an explicit --port wins; otherwise the
	// config-file pin (the brew-services path) applies. A pinned port is
	// deterministic — bus.Start probes it and fails loud if it is unavailable
	// rather than silently rebinding random (the v0.5.1 outage).
	*port = resolvePort(fs, *port, cfg.Port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := bus.Start(ctx, bus.Config{
		StoreDir:            *store,
		Port:                *port,
		LeafListenAddr:      *leafListen,
		WebSocketListenAddr: *wsListen,
		LeafRemoteURL:       *leafRemote,
		LeafCreds:           *leafCreds,
		LeafBundle:          *leafBundle,
		// Surface the bus's diagnostics (notably the loud random-port fallback) on
		// our stderr so an operator running `up` sees a port change rather than
		// being silently stranded. The default would also write to stderr; wiring it
		// explicitly with a prefix keeps the source unambiguous.
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "sextant: "+format+"\n", args...)
		},
	})
	if err != nil {
		fatal("%v", err)
	}

	infoPath := filepath.Join(*store, conninfo.DefaultFile)
	// Record the WebSocket URL alongside the client URL when the listener is on
	// (ADR-0044), so the browser dash discovers where to dial deterministically
	// across restarts. ws:// scheme; the configured loopback host:port is what the
	// listener bound (a pinned port survives a restart — the recommended dash path).
	info := conninfo.Info{URL: b.ClientURL()}
	if *wsListen != "" {
		info.WSURL = "ws://" + *wsListen
	}
	if err := conninfo.Write(infoPath, info); err != nil {
		b.Shutdown()
		fatal("write discovery file: %v", err)
	}

	if *leafRemote != "" {
		// Leaf mode: no local minting/operator credential — local agents connect with
		// hub-minted creds, and the engine lives at the hub. Print the loopback client
		// listener and the hub it links to.
		fmt.Printf("sextant leaf up\n  url:        %s\n  discovery:  %s\n  hub:        %s\n\n"+
			"local agents connect here with hub-minted creds; JetStream stays at the hub.\n\n"+
			"Ctrl-C to stop.\n",
			b.ClientURL(), infoPath, *leafRemote)
		<-ctx.Done()
		stop()
		b.Shutdown()
		fmt.Println("leaf down")
		return
	}

	leafNote := ""
	if *leafListen != "" {
		leafNote = fmt.Sprintf("\nleaf listener: %s — carry these to the remote box (the bundle is public; the link is secret):\n"+
			"  bundle: %s\n  link:   %s\n  (link MUST ride a secure transport — SSH-R / Tailscale / WireGuard)\n"+
			"  note: a hub restart mints a NEW link credential — re-carry leaf-link.creds to the remote box.\n",
			*leafListen, bus.LeafBundlePath(*store), bus.LeafLinkCredsPath(*store))
	}
	if *wsListen != "" {
		leafNote += fmt.Sprintf("\nWebSocket listener: ws://%s — the browser dash connects here as a co-equal TS client (ADR-0044).\n"+
			"  loopback-only + NoTLS; behind the operator's secure transport. The dash mints each tab a short-lived scoped credential.\n",
			*wsListen)
	}

	fmt.Printf("sextant bus up\n  url:        %s\n  discovery:  %s\n  operator:   %s\n%s\n"+
		"issue a client identity (the bus mints it; keys stay in the bus):\n"+
		"  sextant clients register <name> --store %s\n\n"+
		"Ctrl-C to drain and stop.\n",
		b.ClientURL(), infoPath, bus.OperatorCredsPath(*store), leafNote, *store)

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
// defaultStore is the store dir a command uses when --store is not given:
// $SEXTANT_STORE if set, else a per-user config path. An explicit --store still
// overrides this, since flag parsing replaces the default when the flag is
// present.
func defaultStore() string {
	if v := os.Getenv("SEXTANT_STORE"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant", "jetstream")
	}
	return filepath.Join(".sextant", "jetstream")
}

// resolveLeafListen applies the leaf-listen precedence for `up`:
//
//	--leaf-listen flag  >  $SEXTANT_LEAF_LISTEN  >  config-file value  >  "" (off)
//
// The flag wins only when it was EXPLICITLY passed (flag.Visit) — a default-empty
// flag must not mask the env/config sources, but an explicit --leaf-listen=""
// (or any value) is a deliberate override and takes precedence. The address is
// not validated here; the bus validates it when wiring the listener, so a bad
// value still fails `up` loudly (and an empty result is the default-off case).
func resolveLeafListen(fs *flag.FlagSet, flagVal, envVal, cfgVal string) string {
	flagSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "leaf-listen" {
			flagSet = true
		}
	})
	switch {
	case flagSet:
		return flagVal
	case envVal != "":
		return envVal
	default:
		return cfgVal
	}
}

// resolveWebSocketListen applies the ws-listen precedence for `up` (ADR-0044),
// the leaf-listen rule for the bus WebSocket listener:
//
//	--ws-listen flag  >  $SEXTANT_WS_LISTEN  >  config-file value  >  "" (off)
//
// The flag wins only when EXPLICITLY passed (flag.Visit) — a default-empty flag
// must not mask the env/config sources, but an explicit --ws-listen="" (or any
// value) is a deliberate override. The address is not validated here; the bus
// validates it when wiring the listener (a non-loopback address fails `up`
// loudly), and an empty result is the default-off case.
func resolveWebSocketListen(fs *flag.FlagSet, flagVal, envVal, cfgVal string) string {
	flagSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ws-listen" {
			flagSet = true
		}
	})
	switch {
	case flagSet:
		return flagVal
	case envVal != "":
		return envVal
	default:
		return cfgVal
	}
}

// resolvePort applies the listen-port precedence for `up`:
//
//	--port flag (when explicitly passed)  >  config-file port  >  0 (auto)
//
// The flag wins only when EXPLICITLY passed (flag.Visit) — a default --port=0
// must not mask a config pin, but an explicit --port=0 (or any value) is a
// deliberate override. 0 is the auto case (reuse the recorded port if free, else
// random); a non-zero result is a deterministic pin the bus fails loud on if
// taken.
func resolvePort(fs *flag.FlagSet, flagVal, cfgVal int) int {
	flagSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			flagSet = true
		}
	})
	if flagSet {
		return flagVal
	}
	return cfgVal
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sextant: "+format+"\n", args...)
	os.Exit(1)
}

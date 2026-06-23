package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
)

// The principal subcommands (ADR-0030). The principal is the one human's client,
// per bus, whose messages other clients act on as their own operator's direct
// input. The designation lives in a client-readable, Operator-writable sx key:
//   - `principal set <ulid>` is operator-only — it connects with the operator
//     credential `sextant up` provisioned, and the bus rejects the call from any
//     other identity. This is the two-way door: the operator can re-designate.
//   - `principal get` reads the current designation as any client — proof that a
//     client-tier credential can read it but (lacking the set command's operator
//     credential) cannot write it.
//
// These are an extension over the locked core, not protocol operations, so they
// have no entry in protocol/methods.json or the CLI/operation conformance map.

func cmdPrincipal(args []string) {
	if len(args) < 1 {
		fatal("usage: sextant principal set|get ...")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "set":
		principalSet(rest)
	case "get":
		principalGet(rest)
	default:
		fatal("unknown principal verb %q (set|get)", verb)
	}
}

// principalSet points the principal at a client ULID (operator-only). It
// connects with the operator credential and asks the bus to set the designation.
// Re-pointing an already-established principal is deliberate: the bus refuses it
// without --force (ADR-0031), and the command prints the current → new move so a
// re-point is never silent at the keyboard either.
func principalSet(args []string) {
	var ulid string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		ulid, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("principal set", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir: discovery + operator credential (or set $SEXTANT_STORE)")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	force := fs.Bool("force", false, "re-point an already-established principal (required to move it; not needed for the first claim)")
	_ = fs.Parse(args)
	if ulid == "" {
		fatal("usage: sextant principal set <ulid> [--force] [--store DIR] [--url U]")
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
	defer func() { _ = iss.Close() }()
	current, _ := iss.GetPrincipal(ctx) // best-effort, for the print; the set is what matters
	if err := iss.SetPrincipal(ctx, ulid, *force); err != nil {
		fatal("%v", err)
	}
	if current != "" && current != ulid {
		fmt.Printf("principal: %s -> %s\n", current, ulid)
	} else {
		fmt.Printf("principal set to %s\n", ulid)
	}
}

// principalGet reads the current principal as any client (proof of the
// read-open half of the key's shape). It connects with the resolved client
// credential, the same as the other read commands.
func principalGet(args []string) {
	fs := flag.NewFlagSet("principal get", flag.ExitOnError)
	cf := addConnFlags(fs)
	_ = fs.Parse(args)

	ctx := context.Background()
	c := cf.connect(ctx)
	defer func() { _ = c.Close() }()
	principal, err := c.GetPrincipal(ctx)
	if err != nil {
		fatal("%v", err)
	}
	if principal == "" {
		fmt.Println("(no principal designated)")
		return
	}
	fmt.Println(principal)
}

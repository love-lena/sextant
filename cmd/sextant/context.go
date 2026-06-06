package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/conninfo"
)

// cmdContext manages saved client contexts (ADR-0021): local (bus URL + identity
// + creds) profiles, so the everyday commands need no connection flags once one
// is active. These are local-administration commands, not protocol operations —
// like `up`, they sit outside the verb surface and are absent from methods.json
// (and so from the conformance test's cliOperations table).
func cmdContext(args []string) {
	if len(args) < 1 {
		contextUsage()
		os.Exit(2)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		contextList(rest)
	case "add":
		contextAdd(rest)
	case "use":
		contextUse(rest)
	case "current":
		contextCurrent(rest)
	case "delete":
		contextDelete(rest)
	case "-h", "--help", "help":
		contextUsage()
	default:
		fatal("unknown context verb %q (list|add|use|current|delete)", verb)
	}
}

func contextUsage() {
	fmt.Fprint(os.Stderr, `usage: sextant context <command>

  list                       list saved contexts (the active one marked *)
  add <name> --creds F       save a context (URL from --url or --store discovery)
  use <name>                 make <name> the active context
  current                    print the active context name
  delete <name> [--purge]    remove a context (--purge also deletes its creds)

A context bundles a bus URL + identity + creds under a local name, so
publish/subscribe/etc. need no --creds/--url once one is active (ADR-0021).
Select per-command with --context, or set $SEXTANT_CONTEXT.
`)
}

// contextList prints saved contexts, marking the active one with `*`.
func contextList(args []string) {
	fs := flag.NewFlagSet("context list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the contexts as JSON")
	_ = fs.Parse(args)

	ctxs, err := clictx.List()
	if err != nil {
		fatal("%v", err)
	}
	active := clictx.Active()
	if *asJSON {
		type row struct {
			Name    string `json:"name"`
			ID      string `json:"id,omitempty"`
			URL     string `json:"url"`
			Kind    string `json:"kind,omitempty"`
			Display string `json:"display,omitempty"`
			Creds   string `json:"creds"`
			Active  bool   `json:"active"`
		}
		rows := make([]row, 0, len(ctxs))
		for _, c := range ctxs {
			rows = append(rows, row{c.Name, c.ID, c.URL, c.Kind, c.Display, c.Creds, c.Name == active})
		}
		emitJSON(rows)
		return
	}
	if len(ctxs) == 0 {
		fmt.Fprintln(os.Stderr, "no contexts (create one with `sextant context add <name> --creds <file>`)")
		return
	}
	for _, c := range ctxs {
		marker := " "
		if c.Name == active {
			marker = "*"
		}
		id := c.ID
		if id == "" {
			id = "-"
		}
		fmt.Printf("%s %-20s  %-26s  %s\n", marker, c.Name, id, c.URL)
	}
}

// contextAdd saves a context from a credentials file. The creds are copied into
// the config store (so the context is self-contained), and the bus URL comes
// from --url or, failing that, the --store discovery file. The new context
// becomes active if --active is given or none is active yet.
func contextAdd(args []string) {
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("context add", flag.ExitOnError)
	creds := fs.String("creds", "", "credentials file to save (required)")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	store := fs.String("store", defaultStore(), "bus store dir for URL discovery (or set $SEXTANT_STORE)")
	id := fs.String("id", "", "the client's ULID, for reference (optional)")
	display := fs.String("display", "", "display name, for reference (optional)")
	kind := fs.String("kind", "", "what the client is, for reference (optional)")
	active := fs.Bool("active", false, "make this the active context")
	force := fs.Bool("force", false, "replace an existing context of the same name")
	_ = fs.Parse(args)

	if name == "" {
		fatal("usage: sextant context add <name> --creds <file> [--url U]")
	}
	if *creds == "" {
		fatal("context add needs --creds <file>")
	}
	if !*force {
		if _, err := clictx.Load(name); err == nil {
			fatal("context %q already exists (use --force to replace it)", name)
		}
	}
	blob, err := os.ReadFile(*creds)
	if err != nil {
		fatal("read creds: %v", err)
	}
	credsPath, err := clictx.WriteCreds(name, string(blob))
	if err != nil {
		fatal("%v", err)
	}
	busURL := *url
	if busURL == "" {
		if info, err := conninfo.Read(filepath.Join(*store, conninfo.DefaultFile)); err == nil {
			busURL = info.URL
		}
	}
	if err := clictx.Save(clictx.Context{
		Name: name, URL: busURL, ID: *id, Display: *display, Kind: *kind, Creds: credsPath,
	}); err != nil {
		fatal("%v", err)
	}

	makeActive := *active || clictx.Active() == ""
	if makeActive {
		if err := clictx.SetActive(name); err != nil {
			fatal("%v", err)
		}
	}
	if busURL == "" {
		fmt.Fprintln(os.Stderr, "warning: no bus URL recorded (no --url and no discovery file under --store)")
	}
	suffix := ""
	if makeActive {
		suffix = " (now active)"
	}
	fmt.Printf("context %q saved%s\n  creds: %s\n", name, suffix, credsPath)
}

// contextUse makes a saved context the active one.
func contextUse(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fatal("usage: sextant context use <name>")
	}
	name := args[0]
	if err := clictx.SetActive(name); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("active context: %s\n", name)
}

// contextCurrent prints the active context name (for shells/prompts). It exits
// non-zero when there is none, so scripts can branch on it.
func contextCurrent(args []string) {
	_ = args
	name := clictx.Active()
	if name == "" {
		fmt.Fprintln(os.Stderr, "no active context (set one with `sextant context use <name>`)")
		os.Exit(1)
	}
	fmt.Println(name)
}

// contextDelete removes a context; --purge also deletes its saved creds file.
func contextDelete(args []string) {
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("context delete", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete the saved creds file")
	_ = fs.Parse(args)
	if name == "" {
		fatal("usage: sextant context delete <name> [--purge]")
	}

	credsPath := ""
	if c, err := clictx.Load(name); err == nil {
		credsPath = c.Creds
	}
	if err := clictx.Delete(name); err != nil {
		fatal("%v", err)
	}
	if *purge && credsPath != "" {
		if err := os.Remove(credsPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: could not remove creds %s: %v\n", credsPath, err)
		}
	}
	fmt.Printf("deleted context %q\n", name)
}

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/love-lena/sextant/pkg/buscfg"
)

// cmdConfig manages the bus config file `sextant up` reads on startup (the
// brew-services path for settings that must survive a launchd restart). It is a
// thin writer/reader over pkg/buscfg; the file lives in the store dir beside
// bus.json, resolved by the same --store/$SEXTANT_STORE rule `up` uses.
//
//	sextant config set leaf-listen <addr>   write the hub leaf listener address
//	sextant config set leaf-listen ""       clear it (back to default-off)
//	sextant config get [leaf-listen]        print the current config
func cmdConfig(args []string) {
	if len(args) == 0 {
		fatal("config: expected a subcommand (set|get) — see `sextant help`")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "set":
		cmdConfigSet(rest)
	case "get":
		cmdConfigGet(rest)
	default:
		fatal("config: unknown subcommand %q (want set|get)", sub)
	}
}

func cmdConfigSet(args []string) {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir holding the config (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	if err := runConfigSet(os.Stdout, *store, fs.Args()); err != nil {
		fatal("%v", err)
	}
}

// runConfigSet is the testable core: it sets a single config key, preserving any
// other keys already on disk. Only leaf-listen is settable in v0.5.1. The value
// is NOT validated here — the bus validates the leaf address on `up`, so this
// stays a plain writer and there is a single validation site.
func runConfigSet(stdout io.Writer, store string, kv []string) error {
	if len(kv) != 2 {
		return fmt.Errorf("config set: usage: sextant config set leaf-listen <addr>")
	}
	key, val := kv[0], kv[1]
	if key != "leaf-listen" {
		return fmt.Errorf("config set: unknown key %q (only leaf-listen is settable)", key)
	}
	path := buscfg.Path(store)
	cfg, err := buscfg.Load(path)
	if err != nil {
		return err // malformed existing config: fail loud, do not clobber
	}
	cfg.LeafListen = val
	if err := buscfg.Save(path, cfg); err != nil {
		return err
	}
	if val == "" {
		fmt.Fprintf(stdout, "leaf-listen cleared in %s (leaf listener OFF on next `sextant up`)\n", path)
	} else {
		fmt.Fprintf(stdout, "leaf-listen = %s in %s\n  restart the bus to apply: brew services restart sextant\n", val, path)
	}
	return nil
}

func cmdConfigGet(args []string) {
	fs := flag.NewFlagSet("config get", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir holding the config (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	if err := runConfigGet(os.Stdout, *store, fs.Args()); err != nil {
		fatal("%v", err)
	}
}

// runConfigGet prints the config. With no key it prints all known keys; with
// `leaf-listen` it prints just that value (empty = unset / default-off).
func runConfigGet(stdout io.Writer, store string, keys []string) error {
	path := buscfg.Path(store)
	cfg, err := buscfg.Load(path)
	if err != nil {
		return err
	}
	switch {
	case len(keys) == 0:
		fmt.Fprintf(stdout, "config: %s\n  leaf-listen = %s\n", path, quoteEmpty(cfg.LeafListen))
	case len(keys) == 1 && keys[0] == "leaf-listen":
		fmt.Fprintln(stdout, cfg.LeafListen)
	default:
		return fmt.Errorf("config get: unknown key %q (only leaf-listen is known)", keys[0])
	}
	return nil
}

// quoteEmpty renders an empty value as a visible marker so `config get` does not
// print a bare blank for an unset key.
func quoteEmpty(s string) string {
	if s == "" {
		return `"" (unset — leaf listener off)`
	}
	return s
}

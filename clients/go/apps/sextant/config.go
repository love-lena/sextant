package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/love-lena/sextant/bus/buscfg"
)

// cmdConfig manages the bus config file `sextant up` reads on startup (the
// brew-services path for settings that must survive a launchd restart). It is a
// thin writer/reader over pkg/buscfg; the file lives in the store dir beside
// bus.json, resolved by the same --store/$SEXTANT_STORE rule `up` uses.
//
//	sextant config set leaf-listen <addr>   write the hub leaf listener address
//	sextant config set leaf-listen ""       clear it (back to default-off)
//	sextant config set port <n>             pin the bus listen port (0 clears it)
//	sextant config get [leaf-listen|port]   print the current config
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
// other keys already on disk. Settable keys: leaf-listen (an address; the bus
// validates it on `up`) and port (an integer the bus pins deterministically).
// Values are not range-validated here beyond port being a parseable int — the
// bus is the single validation site (it probes the port and fails loud if taken).
func runConfigSet(stdout io.Writer, store string, kv []string) error {
	if len(kv) != 2 {
		return fmt.Errorf("config set: usage: sextant config set leaf-listen <addr> | port <n>")
	}
	key, val := kv[0], kv[1]
	path := buscfg.Path(store)
	cfg, err := buscfg.Load(path)
	if err != nil {
		return err // malformed existing config: fail loud, do not clobber
	}
	switch key {
	case "leaf-listen":
		cfg.LeafListen = val
		if err := buscfg.Save(path, cfg); err != nil {
			return err
		}
		if val == "" {
			_, _ = fmt.Fprintf(stdout, "leaf-listen cleared in %s (leaf listener OFF on next `sextant up`)\n", path)
		} else {
			_, _ = fmt.Fprintf(stdout, "leaf-listen = %s in %s\n  restart the bus to apply: brew services restart sextant\n", val, path)
		}
	case "port":
		n, perr := strconv.Atoi(val)
		if perr != nil || n < 0 || n > 65535 {
			return fmt.Errorf("config set: port must be an integer 0-65535 (0 clears the pin), got %q", val)
		}
		cfg.Port = n
		if err := buscfg.Save(path, cfg); err != nil {
			return err
		}
		if n == 0 {
			_, _ = fmt.Fprintf(stdout, "port cleared in %s (bus picks the recorded-or-random port on next `sextant up`)\n", path)
		} else {
			_, _ = fmt.Fprintf(stdout, "port = %d in %s\n  restart the bus to apply: brew services restart sextant\n", n, path)
		}
	default:
		return fmt.Errorf("config set: unknown key %q (settable: leaf-listen, port)", key)
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
		_, _ = fmt.Fprintf(stdout, "config: %s\n  leaf-listen = %s\n  port        = %s\n", path, quoteEmpty(cfg.LeafListen), quotePort(cfg.Port))
	case len(keys) == 1 && keys[0] == "leaf-listen":
		_, _ = fmt.Fprintln(stdout, cfg.LeafListen)
	case len(keys) == 1 && keys[0] == "port":
		_, _ = fmt.Fprintln(stdout, cfg.Port)
	default:
		return fmt.Errorf("config get: unknown key %q (known: leaf-listen, port)", keys[0])
	}
	return nil
}

// quotePort renders an unset port (0) with a visible marker, like quoteEmpty.
func quotePort(n int) string {
	if n == 0 {
		return `0 (unset — recorded-or-random port)`
	}
	return strconv.Itoa(n)
}

// quoteEmpty renders an empty value as a visible marker so `config get` does not
// print a bare blank for an unset key.
func quoteEmpty(s string) string {
	if s == "" {
		return `"" (unset — leaf listener off)`
	}
	return s
}

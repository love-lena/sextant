package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/internal/components"
)

// `sextant components` manages the agent RUNTIMES — the dispatcher and the
// workflow coordinator — as keep-alive, OS-managed background services, so an
// operator never hunts a pid. The mechanism + the launchd plane live in
// internal/components; this file is the thin CLI face plus the `exec`
// indirection's resolution side.
//
//	sextant components status  [name]
//	sextant components start    [name | --all]
//	sextant components stop     [name | --all]
//	sextant components restart  [name | --all]
//	sextant components exec     <name>          (internal: the plist's entry point)
//
// The plist for a component runs `sextant components exec <name>` (the exec
// INDIRECTION), so launchd launches THIS binary; exec then resolves the env in
// Go and syscall.Execs the real sextant-<name>. That solves launchd's minimal
// PATH (the dispatcher's recipe shells out to `claude`, off launchd's PATH) in
// one testable Go path rather than a plist-embedded shell.
func cmdComponents(args []string) {
	if len(args) == 0 {
		fatal("usage: sextant components status|start|stop|restart [name | --all]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		componentsStatus(rest)
	case "start":
		componentsAction(rest, "start")
	case "stop":
		componentsAction(rest, "stop")
	case "restart":
		componentsAction(rest, "restart")
	case "exec":
		componentsExec(rest)
	default:
		fatal("components: unknown subcommand %q (status|start|stop|restart)", sub)
	}
}

// positional returns the first non-flag arg (the component name), else "".
func positional(args []string) string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0]
	}
	return ""
}

func componentsStatus(args []string) {
	fs := flag.NewFlagSet("components status", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	if !components.Supported() {
		fmt.Fprintln(os.Stderr, unsupportedMsg())
		return
	}
	sel, err := components.Select(positional(fs.Args()), false, false)
	if err != nil {
		fatal("%v", err)
	}
	self, err := os.Executable()
	if err != nil {
		fatal("resolve self: %v", err)
	}
	mgr, err := components.NewManager(self)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("sextant components\n  store: %s\n", *store)
	for _, c := range sel {
		reportComponent(c, mgr)
	}
}

// reportComponent prints one component's status line: installed? loaded?
// running? — mirroring doctor.go's bus state check, but a runtime is "up" when
// its launchd job has a live process (not a TCP listener).
func reportComponent(c components.Component, mgr *components.Manager) {
	binPath, binErr := exec.LookPath(c.Binary)
	installed := "MISSING"
	if binErr == nil {
		installed = binPath
	}
	st, perr := mgr.Status(c.Name)
	switch {
	case binErr != nil:
		fmt.Printf("  %-10s binary: %s — install it before starting\n", c.Name, installed)
	case perr != nil:
		fmt.Printf("  %-10s binary: %s  launchd: query error — %v\n", c.Name, installed, perr)
	case !st.Loaded:
		fmt.Printf("  %-10s binary: %s  service: NOT installed (run `sextant components start %s`)\n", c.Name, installed, c.Name)
	case st.Running:
		fmt.Printf("  %-10s binary: %s  service: loaded + RUNNING\n", c.Name, installed)
	default:
		fmt.Printf("  %-10s binary: %s  service: loaded but NOT running (state=%q) — `sextant components restart %s`\n", c.Name, installed, st.Raw, c.Name)
	}
}

func componentsAction(args []string, action string) {
	fs := flag.NewFlagSet("components "+action, flag.ExitOnError)
	all := fs.Bool("all", false, "act on every managed component")
	store := fs.String("store", defaultStore(), "bus store dir (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	if !components.Supported() {
		fmt.Fprintln(os.Stderr, unsupportedMsg())
		os.Exit(1)
	}
	sel, err := components.Select(positional(fs.Args()), *all, true)
	if err != nil {
		fatal("%v", err)
	}
	self, err := os.Executable()
	if err != nil {
		fatal("resolve self: %v", err)
	}
	mgr, err := components.NewManager(self)
	if err != nil {
		fatal("%v", err)
	}

	failed := false
	for _, c := range sel {
		var err error
		switch action {
		case "start":
			err = startComponent(c, mgr, *store)
		case "stop":
			err = mgr.Stop(os.Stdout, c.Name)
		case "restart":
			if err = mgr.Stop(os.Stdout, c.Name); err == nil {
				err = startComponent(c, mgr, *store)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sextant: %s %s: %v\n", action, c.Name, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// startComponent is the install-side of a component: ensure first-run identity,
// resolve the launchd env (fail loud if a dispatcher's claude is missing),
// materialize the recipe, and write+bootstrap+kickstart+health-check the plist.
func startComponent(c components.Component, mgr *components.Manager, store string) error {
	if _, err := exec.LookPath(c.Binary); err != nil {
		return fmt.Errorf("%s not found on PATH — install sextant's binaries first (%w)", c.Binary, err)
	}
	// First-run identity (before resolving env so a missing bus fails here, not
	// after writing a plist): mint the component's own creds once.
	if err := ensureIdentity(c, store); err != nil {
		return err
	}
	// Resolve the launchd env at start time (where the interactive PATH exists);
	// a dispatcher with no `claude` on PATH fails loud rather than writing a plist
	// that cannot spawn.
	env, err := components.ResolveEnv(mgr.Self, exec.LookPath, c.NeedsClaude)
	if err != nil {
		return err
	}
	if c.NeedsRecipe {
		if _, err := components.WriteRecipe(); err != nil {
			return err
		}
	}
	return mgr.Install(os.Stdout, os.Stderr, c.Name, env)
}

// ensureIdentity ensures the component has its own bus creds at
// components.CredsPath(name), minting once via the held-mode register core (the
// operator credential under store) and recording a NON-active context under the
// handle "component-<name>" so the operator's active context is never disturbed.
// On later starts the creds file already exists and is reused.
func ensureIdentity(c components.Component, store string) error {
	credsPath := components.CredsPath(c.Name)
	if fileExists(credsPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(credsPath), 0o700); err != nil {
		return fmt.Errorf("create components dir: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id, path, err := registerHeld(ctx, store, "", c.Name, c.Kind, credsPath)
	if err != nil {
		return fmt.Errorf("mint %s identity (is the bus up?): %w", c.Name, err)
	}
	// Record a non-active context for visibility (`sextant context list`), pinned
	// to the creds file we wrote. It never becomes active.
	handle := "component-" + c.Name
	url := busURL(store) // recorded bus URL, for context visibility only
	if cerr := clictx.Save(clictx.Context{
		Name: handle, URL: url, ID: id, Display: c.Name, Kind: c.Kind, Creds: path,
	}); cerr != nil {
		// The creds file is what the service uses; a context-save failure is
		// non-fatal (visibility only).
		fmt.Fprintf(os.Stderr, "  %s: warning: could not record context %q: %v\n", c.Name, handle, cerr)
	}
	fmt.Printf("  %s: minted bus identity %s (creds %s)\n", c.Name, id, path)
	return nil
}

// componentsExec is the exec INDIRECTION's resolution side and the plist's entry
// point. It resolves the launchd env in Go, applies it to this process's
// environment, and syscall.Execs the real sextant-<name> with the component's
// flags — so the runtime inherits a full PATH + SEXTANT_MCP_BIN even though
// launchd launched us with a minimal one.
func componentsExec(args []string) {
	fs := flag.NewFlagSet("components exec", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	name := positional(fs.Args())
	if name == "" {
		fatal("usage: sextant components exec <name>")
	}
	c, ok := components.Find(name)
	if !ok {
		fatal("unknown component %q (known: %v)", name, components.Names())
	}
	binPath, err := exec.LookPath(c.Binary)
	if err != nil {
		fatal("%s not found on PATH: %v", c.Binary, err)
	}
	self, err := os.Executable()
	if err != nil {
		fatal("resolve self: %v", err)
	}
	env, err := components.ResolveEnv(self, exec.LookPath, c.NeedsClaude)
	if err != nil {
		fatal("%v", err)
	}
	// Apply the resolved env onto our own environment so the re-exec'd runtime
	// (and any `claude` it spawns) inherits the full PATH + SEXTANT_MCP_BIN.
	for k, v := range env.Map() {
		_ = os.Setenv(k, v)
	}

	creds := components.CredsPath(name)
	recipe := ""
	if c.NeedsRecipe {
		// Re-materialize on exec too, so a fresh launchd start always has the recipe
		// even if the components dir was cleared between install and run.
		if recipe, err = components.WriteRecipe(); err != nil {
			fatal("%v", err)
		}
	}
	argv := append([]string{binPath}, c.Args(creds, *store, recipe)...)
	if err := syscall.Exec(binPath, argv, os.Environ()); err != nil {
		fatal("exec %s: %v", c.Binary, err)
	}
}

// unsupportedMsg is the non-macOS guidance.
func unsupportedMsg() string {
	_, err := components.NewManager("")
	if err != nil {
		return "sextant components: " + err.Error()
	}
	return "sextant components: not supported on this OS"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

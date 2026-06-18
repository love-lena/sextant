package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/internal/selfenroll"
)

// `sextant components` manages the agent RUNTIMES — the dispatcher and the
// workflow coordinator — as keep-alive, OS-managed background services, so an
// operator never has to hunt a pid. Homebrew allows exactly one service per
// formula and the bus already is it (homebrew.mxcl.sextant runs `sextant up`),
// so this command stands up the OTHER components itself: a per-component
// LaunchAgent under ~/Library/LaunchAgents/, bootstrapped + kickstarted into the
// user's gui domain (see launchd.go). The bus is NOT touched here.
//
//	sextant components status [name]
//	sextant components start   [name | --all]
//	sextant components stop    [name | --all]
//	sextant components restart [name | --all]
//
// Two delicate bits, both handled at start time:
//   - launchd PATH: launchd's environment is minimal, but the dispatcher's spawn
//     recipe shells out to `claude` and needs SEXTANT_MCP_BIN. So we DISCOVER the
//     real paths (exec.LookPath, the brew bin, the sibling sextant-mcp) and BAKE
//     them into the generated plist's EnvironmentVariables — never hardcoded.
//   - first-run identity: a component with no bus identity is enrolled once
//     (selfenroll.EnrollAgent, a non-active agent context), and the saved creds
//     are baked into the plist so the launchd service connects as itself.

// agentRecipe is the default dispatcher harness, embedded so a Homebrew install
// (which ships only the binaries) still has it. It is a byte-for-byte copy of
// cmd/sextant-dispatch/recipes/agent.sh — components_test.go's drift guard keeps
// them identical, so the source recipe stays the single source of truth.
//
//go:embed embed/agent.sh
var embeddedRecipes embed.FS

// component is one managed runtime: its service name, the binary that runs it,
// the bus identity it enrolls as, and how its launch args + environment are
// built. The args/env builders take the resolved store + creds + recipe path so
// the same spec drives both the plist write and a status report.
type component struct {
	name        string // service name + context handle suffix (e.g. "dispatch")
	binary      string // sibling binary basename (e.g. "sextant-dispatch")
	kind        string // bus kind to enroll the identity as
	display     string // bus display name for the enrolled identity
	needsRecipe bool   // true if the component needs the embedded agent recipe on disk
	needsClaude bool   // true if the runtime's recipe spawns `claude` — start fails loud if it is not on PATH
	// args builds the ProgramArguments after the binary: the resolved flags. binPath
	// is the discovered binary path; creds is the component's own creds file; store
	// is the bus store; recipe is the on-disk recipe path ("" when !needsRecipe).
	args func(binPath, creds, store, recipe string) []string
}

// components is the registry of managed runtimes. The dash, violet, and other
// surfaces are deliberately NOT here — this slice is the framework plus the two
// pure runtimes (S5-core); the rest land in follow-up slices.
var components = []component{
	{
		name:        "dispatch",
		binary:      "sextant-dispatch",
		kind:        "dispatcher",
		display:     "sextant-dispatch",
		needsRecipe: true,
		needsClaude: true,
		args: func(binPath, creds, store, recipe string) []string {
			// --on-behalf: the dispatcher mints children with its OWN authority
			// (ADR-0033), so it needs no operator credential — the launchd service
			// runs unattended. The harness is the embedded recipe written to disk.
			return []string{
				"--creds", creds, "--store", store,
				"--on-behalf", "--harness", "sh " + recipe,
			}
		},
	},
	{
		name:    "workflow",
		binary:  "sextant-workflow",
		kind:    "workflow",
		display: "sextant-workflow",
		args: func(binPath, creds, store, recipe string) []string {
			// No --plan/--id: listen mode — subscribe to workflow.start and run one
			// coordinator per request (the dash's "start a workflow" path).
			return []string{"--creds", creds, "--store", store}
		},
	},
}

// findComponent returns the registered component by name, or false.
func findComponent(name string) (component, bool) {
	for _, c := range components {
		if c.name == name {
			return c, true
		}
	}
	return component{}, false
}

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
	default:
		fatal("components: unknown subcommand %q (status|start|stop|restart)", sub)
	}
}

// supported reports whether managed components run on this OS. launchctl is
// macOS-only and the live setup is macOS; elsewhere the command degrades to a
// clear message rather than a Linux systemd path (out of scope for this slice).
func supported() bool { return runtime.GOOS == "darwin" }

func unsupportedMsg() string {
	return fmt.Sprintf("sextant components: managed services are not supported on %s yet "+
		"(launchd is macOS-only). Run the components manually, e.g.\n"+
		"  sextant-dispatch --creds F --store DIR --on-behalf --harness 'sh recipe.sh'\n"+
		"  sextant-workflow --creds F --store DIR", runtime.GOOS)
}

// selectComponents resolves a name / --all positional into the components to
// act on. No name and no --all is an error (an explicit choice avoids surprising
// the operator by acting on everything by default).
func selectComponents(args []string, allFlag bool, requireOne bool) ([]component, error) {
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
	}
	switch {
	case allFlag && name != "":
		return nil, fmt.Errorf("pass a name OR --all, not both")
	case allFlag:
		return components, nil
	case name != "":
		c, ok := findComponent(name)
		if !ok {
			return nil, fmt.Errorf("unknown component %q (known: %s)", name, strings.Join(componentNames(), ", "))
		}
		return []component{c}, nil
	case requireOne:
		return nil, fmt.Errorf("name a component or pass --all (known: %s)", strings.Join(componentNames(), ", "))
	default:
		return components, nil // status with no name reports all
	}
}

func componentNames() []string {
	out := make([]string, 0, len(components))
	for _, c := range components {
		out = append(out, c.name)
	}
	sort.Strings(out)
	return out
}

// componentsStatus reports each selected component: is its binary installed, is
// its launchd job loaded, and is it running. With no name it reports all.
func componentsStatus(args []string) {
	fs := flag.NewFlagSet("components status", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	if !supported() {
		fmt.Fprintln(os.Stderr, unsupportedMsg())
		return
	}
	sel, err := selectComponents(fs.Args(), false, false)
	if err != nil {
		fatal("%v", err)
	}
	home, err := userHome()
	if err != nil {
		fatal("%v", err)
	}
	uid := os.Getuid()
	_ = home // resolved to fail loud early if the home dir is unresolvable
	fmt.Printf("sextant components\n  store: %s\n", *store)
	for _, c := range sel {
		reportComponent(os.Stdout, c, uid, launchctl)
	}
}

// reportComponent prints one component's status line: installed? loaded? running?
// — mirroring doctor.go's bus state check but for a runtime (a runtime is "up"
// when its launchd job has a live process, not a TCP listener).
func reportComponent(w io.Writer, c component, uid int, run launchctlRunner) {
	binPath, binErr := exec.LookPath(c.binary)
	installed := "MISSING"
	if binErr == nil {
		installed = binPath
	}
	st, perr := printState(run, uid, c.name)
	switch {
	case binErr != nil:
		fmt.Fprintf(w, "  %-9s binary: %s — install it before starting\n", c.name, installed)
	case perr != nil:
		fmt.Fprintf(w, "  %-9s binary: %s  launchd: query error — %v\n", c.name, installed, perr)
	case !st.Loaded:
		fmt.Fprintf(w, "  %-9s binary: %s  service: NOT installed (run `sextant components start %s`)\n", c.name, installed, c.name)
	case st.Running:
		fmt.Fprintf(w, "  %-9s binary: %s  service: loaded + RUNNING\n", c.name, installed)
	default:
		fmt.Fprintf(w, "  %-9s binary: %s  service: loaded but NOT running (state=%q) — `sextant components restart %s`\n", c.name, installed, st.Raw, c.name)
	}
}

// The polling budget for a component's post-kickstart liveness. A runtime comes
// up fast (connect + subscribe); a short budget catches the loaded-but-runs=0
// trap without a long stall. Vars so a test can shrink them.
var (
	componentHealthBudget = 8 * time.Second
	componentPollInterval = 250 * time.Millisecond
)

// componentsAction runs start/stop/restart over the selected components.
func componentsAction(args []string, action string) {
	fs := flag.NewFlagSet("components "+action, flag.ExitOnError)
	all := fs.Bool("all", false, "act on every managed component")
	store := fs.String("store", defaultStore(), "bus store dir (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	if !supported() {
		fmt.Fprintln(os.Stderr, unsupportedMsg())
		os.Exit(1)
	}
	sel, err := selectComponents(fs.Args(), *all, true)
	if err != nil {
		fatal("%v", err)
	}
	home, err := userHome()
	if err != nil {
		fatal("%v", err)
	}
	uid := os.Getuid()
	failed := false
	for _, c := range sel {
		var err error
		switch action {
		case "start":
			err = startComponent(os.Stdout, os.Stderr, c, home, uid, *store, launchctl)
		case "stop":
			err = stopComponent(os.Stdout, c, uid, launchctl)
		case "restart":
			if err = stopComponent(os.Stdout, c, uid, launchctl); err == nil {
				err = startComponent(os.Stdout, os.Stderr, c, home, uid, *store, launchctl)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sextant: %s %s: %v\n", action, c.name, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// startComponent installs (writes + bootstraps) and kickstarts a component, then
// health-checks it — reusing the #211 lesson (update.go's ensureBusUp): a bare
// bootstrap can leave a job loaded-but-runs=0, so we kickstart + poll for a live
// process and warn LOUDLY if it never comes up rather than report a hollow
// success. It is the discover-then-bake + first-run-identity site:
//   - the binary path, the brew bin, claude's dir, and sextant-mcp are discovered
//     and baked into the plist's PATH + SEXTANT_MCP_BIN;
//   - a component with no bus identity is enrolled once and its creds baked in.
func startComponent(stdout, stderr io.Writer, c component, home string, uid int, store string, run launchctlRunner) error {
	binPath, err := exec.LookPath(c.binary)
	if err != nil {
		return fmt.Errorf("%s not found on PATH — install sextant's binaries first (%w)", c.binary, err)
	}

	// Fail loud BEFORE writing any plist if the runtime spawns `claude` and it is
	// not on PATH (the dispatcher). A claude-less PATH would be baked into the
	// plist and the dispatcher's spawned agents would silently fail to find
	// `claude` at runtime — the exact silent-spawn-failure this design prevents.
	// claude commonly lives at ~/.local/bin/claude, off launchd's default PATH.
	if c.needsClaude {
		if _, lerr := exec.LookPath("claude"); lerr != nil {
			return fmt.Errorf("the %s needs the `claude` CLI on PATH to spawn agents, "+
				"but it was not found — install it (commonly ~/.local/bin/claude) or ensure it is on your PATH, then retry (%w)", c.name, lerr)
		}
	}

	// First-run identity: ensure the component has its own bus creds (enrolled
	// once as a non-active agent context, reattached by Load thereafter).
	creds, err := ensureIdentity(stdout, c, store)
	if err != nil {
		return err
	}

	// Discover-then-bake the recipe (dispatcher only): write the embedded recipe
	// to a stable on-disk path the plist's --harness points at.
	recipe := ""
	if c.needsRecipe {
		recipe, err = ensureRecipe()
		if err != nil {
			return err
		}
	}

	// Discover-then-bake the launchd environment: launchd's PATH is minimal, so
	// bake a PATH covering the brew bin + claude's dir + sextant's own dir, plus
	// SEXTANT_MCP_BIN explicitly (the dispatcher's recipe requires it).
	env := bakeEnv(binPath)

	logPath := componentLogPath(c.name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	spec := plistSpec{
		Label:   launchdLabelFor(c.name),
		Program: append([]string{binPath}, c.args(binPath, creds, store, recipe)...),
		LogPath: logPath,
		Env:     env,
	}
	plist, err := genPlist(spec)
	if err != nil {
		return err
	}
	path := plistPath(home, c.name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	// A restart re-writes the plist (bootout already ran), so an updated binary
	// path or baked env is picked up. bootout-before-bootstrap keeps it idempotent.
	_ = bootout(run, uid, c.name)
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", path, err)
	}
	fmt.Fprintf(stdout, "  %s: wrote %s\n", c.name, path)

	if err := bootstrapAndKickstart(run, uid, c.name, path); err != nil {
		return err
	}
	if pollRunning(run, uid, c.name, componentHealthBudget, componentPollInterval) {
		fmt.Fprintf(stdout, "  %s: started (loaded + running)\n", c.name)
		return nil
	}
	// Loaded but never ran: the post-bootstrap trap. Fail loud with the log + the
	// exact recovery, never a hollow success.
	fmt.Fprintf(stderr, "\n  WARNING: %s was loaded but did NOT come up running.\n", c.name)
	fmt.Fprintf(stderr, "  Check its log: %s\n", logPath)
	fmt.Fprintf(stderr, "  Force a relaunch: launchctl kickstart -k %s\n", guiTarget(uid, c.name))
	return fmt.Errorf("%s did not reach running within %s", c.name, componentHealthBudget)
}

// stopComponent boots a component's job out of the gui domain. It leaves the
// plist on disk (a later start re-bootstraps it). A not-loaded job is success.
func stopComponent(stdout io.Writer, c component, uid int, run launchctlRunner) error {
	if err := bootout(run, uid, c.name); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  %s: stopped\n", c.name)
	return nil
}

// ensureIdentity returns the component's own creds path, enrolling a fresh agent
// identity the first time (selfenroll.EnrollAgent — a non-active context, so it
// never disturbs the operator's active context). On subsequent starts it
// reattaches by loading the saved context. The context handle is deterministic
// per component ("component-<name>") so reattach is exact.
func ensureIdentity(stdout io.Writer, c component, store string) (string, error) {
	handle := "component-" + c.name
	if ctx, err := clictx.Load(handle); err == nil {
		if ctx.Creds == "" {
			return "", fmt.Errorf("context %q has no creds path; delete it and restart to re-enroll", handle)
		}
		return ctx.Creds, nil
	}
	// First run: mint a dedicated identity over the bus enrollment credential.
	cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := selfenroll.EnrollAgent(cctx, handle, c.display, c.kind, "", store)
	if err != nil {
		return "", fmt.Errorf("enroll %s identity (is the bus up?): %w", c.name, err)
	}
	fmt.Fprintf(stdout, "  %s: enrolled bus identity %s (context %q)\n", c.name, res.ID, handle)
	return res.CredsPath, nil
}

// ensureRecipe writes the embedded dispatcher recipe to a stable on-disk path
// (under the context root, beside creds) and returns it. A Homebrew install
// ships only the binaries, so the recipe the dispatcher's --harness needs must
// be materialized from the embed. It is rewritten each start so an upgraded
// recipe is picked up.
func ensureRecipe() (string, error) {
	data, err := embeddedRecipes.ReadFile("embed/agent.sh")
	if err != nil {
		return "", fmt.Errorf("read embedded recipe: %w", err)
	}
	dir := filepath.Join(clictx.Root(), "components")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create recipe dir: %w", err)
	}
	path := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", fmt.Errorf("write recipe %s: %w", path, err)
	}
	return path, nil
}

// componentLogPath is where a component's combined stdout+stderr goes. It lives
// under the context root so it sits beside the component's other state and needs
// no brew var dir (which a non-brew install would not have).
func componentLogPath(name string) string {
	return filepath.Join(clictx.Root(), "logs", name+".log")
}

// bakeEnv builds the EnvironmentVariables the launchd service inherits. launchd's
// PATH is minimal, so we DISCOVER then BAKE: a PATH covering the brew bin,
// claude's directory, and sextant's own directory, plus SEXTANT_MCP_BIN
// explicitly (the dispatcher recipe requires it). Discovery is best-effort —
// a missing claude or sextant-mcp omits that entry rather than failing the
// start (the recipe itself fails loud at spawn time if SEXTANT_MCP_BIN is unset,
// pointing the operator at the fix).
func bakeEnv(sextantBinPath string) map[string]string {
	dirs := []string{}
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	// sextant's own dir (the brew bin, typically) — sibling binaries live here.
	add(filepath.Dir(sextantBinPath))
	if p, err := exec.LookPath("brew"); err == nil {
		add(filepath.Dir(p)) // the brew bin dir
	}
	add("/opt/homebrew/bin")
	add("/usr/local/bin")
	if p, err := exec.LookPath("claude"); err == nil {
		add(filepath.Dir(p))
	}
	add("/usr/bin")
	add("/bin")

	env := map[string]string{"PATH": strings.Join(dirs, ":")}
	// SEXTANT_MCP_BIN: prefer a sibling next to sextant, else PATH lookup.
	if mcp := filepath.Join(filepath.Dir(sextantBinPath), "sextant-mcp"); fileExists(mcp) {
		env["SEXTANT_MCP_BIN"] = mcp
	} else if p, err := exec.LookPath("sextant-mcp"); err == nil {
		env["SEXTANT_MCP_BIN"] = p
	}
	return env
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

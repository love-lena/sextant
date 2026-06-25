package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/love-lena/sextant/bus/buscfg"
	"github.com/love-lena/sextant/clients/sextant-cli/internal/components"
	"github.com/love-lena/sextant/protocol/conninfo"
)

// cmdDoctor is the read-only health command: it diagnoses a bus that won't come
// up. The v0.5.1 outage was hard to recover because `brew services` showed only
// `Running: false` with no reason — doctor surfaces the missing facts (the
// recorded port, whether anything is listening on it, the config pin, and the
// launchd job state) and points at the kickstart recovery for the
// loaded-but-not-running case. It now also reports the managed agent runtimes
// (dispatcher, workflow, and any components in the Registry) so the operator
// sees the full picture — bus + runtimes — in one command. It never starts,
// stops, or mutates anything.
//
//	sextant doctor [--store DIR]
const launchdLabel = "homebrew.mxcl.sextant"

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	store := fs.String("store", defaultStore(), "bus store dir: discovery + config (or set $SEXTANT_STORE)")
	_ = fs.Parse(args)
	runDoctor(os.Stdout, *store)
}

// runDoctor is the testable core: it writes a health report for the store at
// store to w. It is read-only — every check is a file read, a TCP dial, or a
// `launchctl print` (itself read-only). It does not fail the process; an
// unhealthy bus is a report, not an error exit, so the operator always gets the
// full picture. lookPath and compRunner are injected for tests (pass nil to use
// the real exec.LookPath and the real launchctl).
func runDoctor(w io.Writer, store string) {
	runDoctorFull(w, store, nil, nil)
}

func runDoctorFull(w io.Writer, store string, lookPath func(string) (string, error), compRunner components.Runner) {
	_, _ = fmt.Fprintf(w, "sextant doctor\n  store: %s\n", store)

	// Discovery file (bus.json): the recorded URL is what every client resolves
	// to. Its presence + port is the first thing to know.
	infoPath := filepath.Join(store, conninfo.DefaultFile)
	info, err := conninfo.Read(infoPath)
	var recordedURL, recordedHostPort string
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "  discovery (bus.json): MISSING or unreadable — %v\n", err)
		_, _ = fmt.Fprintf(w, "    the bus has not written one (never started?), or the store dir is wrong.\n")
	default:
		recordedURL = info.URL
		recordedHostPort = hostPort(info.URL)
		_, _ = fmt.Fprintf(w, "  discovery (bus.json): %s\n    url: %s\n", infoPath, recordedURL)
	}

	// Config pin (the brew-services path): a deterministic port + leaf state. A
	// pin is what keeps clients reachable across a restart.
	cfg, cerr := buscfg.Load(buscfg.Path(store))
	switch {
	case cerr != nil:
		_, _ = fmt.Fprintf(w, "  config: UNREADABLE — %v (this fails `sextant up` loudly)\n", cerr)
	default:
		port := "0 (unset — recorded-or-random)"
		if cfg.Port != 0 {
			port = fmt.Sprintf("%d (pinned — deterministic across restart)", cfg.Port)
		}
		leaf := "off"
		if cfg.LeafListen != "" {
			leaf = cfg.LeafListen
		}
		ws := "off"
		if cfg.WebSocketListen != "" {
			ws = cfg.WebSocketListen
		}
		_, _ = fmt.Fprintf(w, "  config: port=%s  leaf-listen=%s  ws-listen=%s\n", port, leaf, ws)
		if cfg.Port == 0 {
			_, _ = fmt.Fprintf(w, "    hint: pin a port with `sextant config set port <n>` so clients survive a bus restart.\n")
		}
		if cfg.WebSocketListen == "" {
			_, _ = fmt.Fprintf(w, "    hint: the browser dash needs the WebSocket listener — `sextant config set ws-listen 127.0.0.1:<port>` then restart the bus (ADR-0044).\n")
		}
	}

	// Reachability: is anything actually listening on the recorded address? This
	// is the single most useful fact `brew services` omits.
	if recordedHostPort != "" {
		if reachable(recordedHostPort, 2*time.Second) {
			_, _ = fmt.Fprintf(w, "  reachable: YES — something is listening on %s\n", recordedHostPort)
		} else {
			_, _ = fmt.Fprintf(w, "  reachable: NO — nothing is listening on %s\n", recordedHostPort)
			_, _ = fmt.Fprintf(w, "    the bus is down, or it rebound to a different port (clients pinned to this address are stranded).\n")
		}
	}

	// launchd job (macOS / brew services): loaded vs running. The outage's worst
	// trap — a job loaded-but-never-launched shows `Running: false` with no error.
	reportLaunchd(w)

	// Agent runtimes: each managed component in the Registry — binary installed?
	// service loaded + running? remediation hint if anything's down.
	reportComponents(w, lookPath, compRunner)
}

// reportComponents iterates the managed component Registry and reports each
// component's install + service state, matching doctor's check/output style.
// lookPath and runner are injected (nil = production defaults: exec.LookPath and
// the real launchctl via a fresh Manager). On non-macOS the service check is
// skipped with a clear note — binary-installed is still useful.
func reportComponents(w io.Writer, lookPath func(string) (string, error), runner components.Runner) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, _ = fmt.Fprintf(w, "  runtimes:\n")
	if !components.Supported() {
		_, _ = fmt.Fprintf(w, "    n/a (managed services are macOS-only; binary checks only)\n")
		for _, c := range components.Registry {
			binPath, binErr := lookPath(c.Binary)
			if binErr != nil {
				_, _ = fmt.Fprintf(w, "    %-12s binary: MISSING (%s not on PATH)\n", c.Name, c.Binary)
			} else {
				_, _ = fmt.Fprintf(w, "    %-12s binary: %s\n", c.Name, binPath)
			}
		}
		return
	}
	// On macOS: build a Manager with the injected runner (or the real launchctl).
	var mgr *components.Manager
	if runner != nil {
		// Tests inject a fake runner directly; uid 0 is fine for target-string
		// formatting in tests — real status comes from the runner, not uid.
		mgr = &components.Manager{UID: os.Getuid(), Run: runner}
	} else {
		self, err := os.Executable()
		if err != nil {
			_, _ = fmt.Fprintf(w, "    could not resolve self binary: %v\n", err)
			return
		}
		var merr error
		mgr, merr = components.NewManager(self)
		if merr != nil {
			_, _ = fmt.Fprintf(w, "    could not build component manager: %v\n", merr)
			return
		}
	}
	for _, c := range components.Registry {
		reportDoctorComponent(w, c, mgr, lookPath)
	}
}

// reportDoctorComponent prints one managed-runtime check line, matching the
// `components status` reportComponent output style but writing to an io.Writer
// so doctor's testable core can use it. Remediation hints are printed when
// something is down.
func reportDoctorComponent(w io.Writer, c components.Component, mgr *components.Manager, lookPath func(string) (string, error)) {
	binPath, binErr := lookPath(c.Binary)
	if binErr != nil {
		_, _ = fmt.Fprintf(w, "    %-12s binary: MISSING (%s not on PATH) — install sextant's binaries\n", c.Name, c.Binary)
		return
	}
	st, perr := mgr.Status(c.Name)
	switch {
	case perr != nil:
		_, _ = fmt.Fprintf(w, "    %-12s binary: %s  service: query error — %v\n", c.Name, binPath, perr)
	case !st.Loaded:
		_, _ = fmt.Fprintf(w, "    %-12s binary: %s  service: NOT running — run `sextant components start %s`\n", c.Name, binPath, c.Name)
		if c.NeedsKey {
			_, _ = fmt.Fprintf(w, "      if violet has no key: run `sextant secret set anthropic` first\n")
		}
	case st.Running:
		line := fmt.Sprintf("    %-12s binary: %s  service: loaded + RUNNING", c.Name, binPath)
		// Agent-kind components (violet, Kind="agent") need a bus-online check for
		// full liveness. The service running state is a necessary but not sufficient
		// signal; the bus-enrolled presence is the authoritative signal. For the
		// current slice a best-effort note plus `sextant clients list` is the right
		// level of detail — a direct bus query in doctor adds latency and a new
		// failure surface.
		if c.Kind == "agent" {
			line += " (bus presence: best-effort, see `sextant clients list`)"
		}
		_, _ = fmt.Fprintln(w, line)
	default:
		_, _ = fmt.Fprintf(w, "    %-12s binary: %s  service: loaded but NOT running (state=%q) — run `sextant components restart %s`\n",
			c.Name, binPath, st.Raw, c.Name)
		// For dispatcher specifically, NeedsClaude is a common start-failure reason.
		if c.NeedsClaude {
			_, _ = fmt.Fprintf(w, "      if the dispatcher never started: check `claude` is on PATH\n")
		}
		// For key-bearing components (violet), a crash-loop may mean the key is missing.
		if c.NeedsKey {
			_, _ = fmt.Fprintf(w, "      if violet is crash-looping: run `sextant secret set anthropic`\n")
		}
	}
}

// hostPort extracts the host:port from a nats:// URL for dialing.
func hostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// reachable reports whether a TCP connection to addr succeeds within timeout. It
// is the read-only liveness probe — a successful dial means a listener is there
// (it does not authenticate or speak NATS, just confirms the port is open).
func reachable(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// reportLaunchd reports the brew-services launchd job state on macOS. On other
// platforms (or when launchctl is unavailable) it says so and stops — doctor is
// useful without it. It runs `launchctl print`, which is read-only.
func reportLaunchd(w io.Writer) {
	if runtime.GOOS != "darwin" {
		_, _ = fmt.Fprintf(w, "  launchd: n/a (not macOS — brew services uses launchd only on macOS)\n")
		return
	}
	if _, err := exec.LookPath("launchctl"); err != nil {
		_, _ = fmt.Fprintf(w, "  launchd: launchctl not found on PATH\n")
		return
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
	out, err := exec.Command("launchctl", "print", target).CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		// `launchctl print` exits non-zero both when the job is genuinely not
		// loaded AND when the query itself failed (a permission/domain error). Tell
		// them apart: "Could not find service" (or exit 113) means not loaded;
		// anything else is a query failure the operator should see verbatim rather
		// than be wrongly told to "start the service".
		if exitCode(err) == 113 || strings.Contains(trimmed, "Could not find service") {
			_, _ = fmt.Fprintf(w, "  launchd: job %q NOT LOADED (brew services not started, or different label)\n", launchdLabel)
			_, _ = fmt.Fprintf(w, "    start it: brew services start sextant\n")
			return
		}
		_, _ = fmt.Fprintf(w, "  launchd: could not query the job (%v) — state unknown\n", err)
		if trimmed != "" {
			_, _ = fmt.Fprintf(w, "    launchctl said: %s\n", firstLine(trimmed))
		}
		return
	}
	state, logPath := parseLaunchdState(string(out))
	switch state {
	case "running":
		_, _ = fmt.Fprintf(w, "  launchd: job %q LOADED + running\n", launchdLabel)
	case "":
		_, _ = fmt.Fprintf(w, "  launchd: job %q LOADED (state unknown)\n", launchdLabel)
	default:
		// Loaded but not running (e.g. "waiting", "not running"): the throttle trap.
		_, _ = fmt.Fprintf(w, "  launchd: job %q LOADED but NOT running (state=%q)\n", launchdLabel, state)
		_, _ = fmt.Fprintf(w, "    likely a launchd throttle from rapid restarts. Force a relaunch:\n")
		_, _ = fmt.Fprintf(w, "    launchctl kickstart -k gui/%d/%s\n", os.Getuid(), launchdLabel)
	}
	if logPath != "" {
		_, _ = fmt.Fprintf(w, "    log: %s\n", logPath)
	}
}

// exitCode returns the process exit code from an *exec.ExitError, or -1 when err
// is not an exit error (e.g. launchctl could not be started at all).
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// firstLine returns the first line of s — enough of a launchctl error to show
// without dumping a multi-line dict.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// parseLaunchdState pulls the top-level `state = X` and `stdout path = Y` from
// `launchctl print` output. The job dict nests sub-states (e.g. endpoint
// `state = active`) — we take the FIRST `state =`, which is the job's own.
func parseLaunchdState(out string) (state, logPath string) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if state == "" && strings.HasPrefix(line, "state = ") {
			state = strings.TrimSpace(strings.TrimPrefix(line, "state = "))
			continue
		}
		if logPath == "" && strings.HasPrefix(line, "stdout path = ") {
			logPath = strings.TrimSpace(strings.TrimPrefix(line, "stdout path = "))
		}
	}
	return state, logPath
}

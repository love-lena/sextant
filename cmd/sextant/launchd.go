package main

import (
	"bufio"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// launchd is the macOS service plane `sextant components` writes and supervises.
//
// Homebrew allows exactly one service per formula, and the bus already is it
// (homebrew.mxcl.sextant runs `sextant up`). The OTHER components — the
// dispatcher and the workflow coordinator — are managed here: a per-component
// LaunchAgent plist under ~/Library/LaunchAgents/, bootstrapped + kickstarted
// into the per-user gui domain, mirroring the bus plist's shape (KeepAlive,
// RunAtLoad, StandardOut/ErrPath). The result is "never hunt a pid": a
// component is an OS-managed, keep-alive background service.
//
// Everything in this file is darwin-only in production (launchctl is macOS).
// The plist generation + the launchctl orchestration are split: genPlist is
// pure (no I/O), and the bootstrap/kickstart/health steps take an injected
// runner so tests drive every path without a real launchd.

// componentLabelPrefix namespaces the per-component LaunchAgent labels. The bus
// keeps its Homebrew label (homebrew.mxcl.sextant); the components sit under a
// distinct prefix so they never collide with it.
const componentLabelPrefix = "dev.sextant."

// launchdLabelFor is a component's LaunchAgent label, e.g. "dev.sextant.dispatch".
func launchdLabelFor(name string) string { return componentLabelPrefix + name }

// plistPath is where a component's LaunchAgent plist lives. Per-user agents live
// under ~/Library/LaunchAgents/<label>.plist.
func plistPath(home, name string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabelFor(name)+".plist")
}

// guiTarget is a component's per-user launchd gui-domain target, the same domain
// the bus uses (gui/<uid>/<label>). Mirrors cmd/sextant/doctor.go's bus target.
func guiTarget(uid int, name string) string {
	return fmt.Sprintf("gui/%d/%s", uid, launchdLabelFor(name))
}

// guiDomain is the per-user launchd domain (gui/<uid>) a service is bootstrapped
// into.
func guiDomain(uid int) string { return fmt.Sprintf("gui/%d", uid) }

// plistSpec is the data genPlist renders. It mirrors the bus plist's keys:
// KeepAlive + RunAtLoad make it a keep-alive service that starts on load, and a
// single log path captures stdout+stderr. EnvironmentVariables is where the
// discovered PATH + SEXTANT_MCP_BIN are baked (launchd's own PATH is minimal, so
// a recipe that shells out to `claude` would otherwise fail — discover then bake).
type plistSpec struct {
	Label   string
	Program []string          // ProgramArguments: the binary + its flags
	LogPath string            // StandardOut/ErrPath (combined)
	Env     map[string]string // EnvironmentVariables: baked PATH + SEXTANT_MCP_BIN etc.
}

// plistTemplate mirrors the Homebrew-generated bus plist shape. html/template
// escapes the substituted values so a path with an XML-special char cannot break
// the document.
var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
{{range .Program}}		<string>{{.}}</string>
{{end}}	</array>
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<true/>
{{if .Env}}	<key>EnvironmentVariables</key>
	<dict>
{{range $k, $v := .Env}}		<key>{{$k}}</key>
		<string>{{$v}}</string>
{{end}}	</dict>
{{end}}	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`))

// genPlist renders a LaunchAgent plist for spec. It is pure (no I/O) so a test
// can assert the document shape directly.
func genPlist(spec plistSpec) (string, error) {
	var b strings.Builder
	if err := plistTemplate.Execute(&b, spec); err != nil {
		return "", fmt.Errorf("render plist: %w", err)
	}
	return b.String(), nil
}

// launchctl runs a launchctl subcommand. It is the production launchctlRunner;
// tests inject a fake to drive bootstrap/kickstart/print without a real launchd.
func launchctl(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// launchctlRunner is the injected seam over launchctl. Returning the combined
// output lets callers parse `print` and surface errors verbatim.
type launchctlRunner func(args ...string) (string, error)

// bootstrapAndKickstart loads a plist into the user's gui domain and forces it to
// run. It reuses the #211 lesson (update.go): a bare bootstrap can leave a job
// loaded-but-runs=0, so we always kickstart after — `launchctl kickstart -k`
// (re)launches it. A bootstrap that fails because the label is ALREADY loaded is
// not an error here (a restart re-bootstraps an existing plist); the kickstart
// still forces the relaunch.
func bootstrapAndKickstart(run launchctlRunner, uid int, name, plist string) error {
	if out, err := run("bootstrap", guiDomain(uid), plist); err != nil {
		// Idempotent: "service already loaded" / "already bootstrapped" is fine —
		// we go on to kickstart. Anything else is a real failure.
		if !alreadyLoaded(out) {
			return fmt.Errorf("bootstrap %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
		}
	}
	if out, err := run("kickstart", "-k", guiTarget(uid, name)); err != nil {
		return fmt.Errorf("kickstart %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
	}
	return nil
}

// bootout unloads a component's job from the user's gui domain. A bootout of a
// job that is not loaded is treated as success — stopping an already-stopped
// component is not an error.
func bootout(run launchctlRunner, uid int, name string) error {
	out, err := run("bootout", guiTarget(uid, name))
	if err != nil && !notLoaded(out) {
		return fmt.Errorf("bootout %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
	}
	return nil
}

// alreadyLoaded reports whether a bootstrap failure is the benign
// already-bootstrapped case (so a restart's re-bootstrap is idempotent).
func alreadyLoaded(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "already") || strings.Contains(o, "service already loaded") || strings.Contains(o, "bootstrap failed: 5")
}

// notLoaded reports whether a bootout/print failure means the job simply is not
// loaded (vs a real query error). launchctl uses both a message and exit 113.
func notLoaded(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "could not find") || strings.Contains(o, "no such process") || strings.Contains(o, "not find service")
}

// launchdRunState is what a `launchctl print` query reports for a component.
type launchdRunState struct {
	Loaded  bool   // the job is bootstrapped into the domain
	Running bool   // it has a live process (pid present / runs>0)
	Raw     string // the parsed top-level state string, for display
}

// printState queries a component's job state via `launchctl print` (read-only)
// and parses whether it is loaded and running. A "could not find service" is
// reported as not-loaded (not an error) — the component simply is not installed
// into launchd yet.
func printState(run launchctlRunner, uid int, name string) (launchdRunState, error) {
	out, err := run("print", guiTarget(uid, name))
	if err != nil {
		if notLoaded(out) {
			return launchdRunState{Loaded: false}, nil
		}
		return launchdRunState{}, fmt.Errorf("print %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
	}
	return parsePrint(out), nil
}

// parsePrint pulls the loaded/running facts from `launchctl print` output. A
// printed job is loaded; whether it is running is read from `state = running`
// or a present `pid = N`. The job's own top-level `state` is the FIRST one
// (sub-dicts nest their own), mirroring doctor.go's parseLaunchdState.
func parsePrint(out string) launchdRunState {
	st := launchdRunState{Loaded: true}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if st.Raw == "" && strings.HasPrefix(line, "state = ") {
			st.Raw = strings.TrimSpace(strings.TrimPrefix(line, "state = "))
			if st.Raw == "running" {
				st.Running = true
			}
			continue
		}
		if !st.Running && strings.HasPrefix(line, "pid = ") {
			// A live pid means a running process even if the top-level state line
			// read something else first.
			st.Running = true
		}
	}
	return st
}

// pollRunning polls printState until the component reports running or the budget
// elapses. It is the runtime liveness check: unlike the bus (a TCP listener), a
// runtime is "up" when its launchd job has a live process — registered identity
// + runs>0 — so a loaded-but-runs=0 job (the post-bootstrap trap) is caught and
// surfaced loudly rather than reported as a hollow success.
func pollRunning(run launchctlRunner, uid int, name string, budget, interval time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if st, err := printState(run, uid, name); err == nil && st.Running {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// userHome resolves the user's home dir for the LaunchAgents path. It fails loud
// rather than silently writing a plist to the wrong place.
func userHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "", fmt.Errorf("cannot resolve home dir for ~/Library/LaunchAgents: %w", err)
	}
	return h, nil
}

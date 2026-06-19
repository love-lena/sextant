package components

import (
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// service.go is the testable orchestration core: plist generation (pure) and the
// launchctl bootstrap/kickstart/bootout/print steps, all over an injected Runner
// so tests drive every path without a real launchd. The OS-gated pieces
// (Supported, the real launchctl Runner, the uid/home defaults) live in
// launchd_darwin.go / launchd_other.go.

// LabelPrefix namespaces the per-component LaunchAgent labels. The bus keeps its
// Homebrew label (homebrew.mxcl.sextant); the components sit under this distinct
// prefix so they never collide with it.
const LabelPrefix = "dev.sextant."

// Label is a component's LaunchAgent label, e.g. "dev.sextant.dispatcher".
func Label(name string) string { return LabelPrefix + name }

// PlistPath is where a component's LaunchAgent plist lives, under the user's home.
func PlistPath(home, name string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label(name)+".plist")
}

// GUITarget is a component's per-user launchd gui-domain target (gui/<uid>/<label>),
// the same domain the bus uses. Mirrors cmd/sextant/doctor.go's bus target.
func GUITarget(uid int, name string) string { return fmt.Sprintf("gui/%d/%s", uid, Label(name)) }

func guiDomain(uid int) string { return fmt.Sprintf("gui/%d", uid) }

// Runner runs a launchctl subcommand, returning the combined output. The real
// runner shells out to launchctl (launchd_darwin.go); tests inject a fake.
type Runner func(args ...string) (string, error)

// Manager supervises the per-component LaunchAgents in one user's gui domain. It
// holds the injected Runner + the resolved uid/home/self-binary, so all the
// behaviour is exercised without a real launchd.
type Manager struct {
	UID  int
	Home string
	Self string // the running sextant binary's path (plist ProgramArguments[0])
	Run  Runner
}

// plistSpec is what genPlist renders — the bus plist's keys: KeepAlive +
// RunAtLoad make it a keep-alive service that starts on load, a single log path
// captures stdout+stderr, EnvironmentVariables carries the baked PATH +
// SEXTANT_MCP_BIN, and ProgramArguments is the exec INDIRECTION:
// [<self sextant>, components, exec, <name>].
type plistSpec struct {
	Label   string
	Program []string
	LogPath string
	Env     map[string]string
}

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

// genPlist renders a LaunchAgent plist. It is pure (no I/O); html/template
// escapes the substituted values so a path with an XML-special char cannot
// break the document.
func genPlist(spec plistSpec) (string, error) {
	var b strings.Builder
	if err := plistTemplate.Execute(&b, spec); err != nil {
		return "", fmt.Errorf("render plist: %w", err)
	}
	return b.String(), nil
}

// The poll budget for a component's post-kickstart liveness. A runtime comes up
// fast (connect + subscribe); a short budget catches the loaded-but-runs=0 trap
// without a long stall. Vars so a test can shrink them.
var (
	HealthBudget = 8 * time.Second
	PollInterval = 250 * time.Millisecond
)

// PollUntil polls cond until it returns true or the budget elapses. It is the
// shared bounded-poll loop behind the runtime liveness check and the bus
// ensure-up health check (cmd/sextant/update.go reuses it) — the loaded-but-
// runs=0 lesson (#211): never trust a bootstrap/kickstart to have actually
// launched; poll for the real signal, then warn loud if it never comes.
func PollUntil(cond func() bool, budget, interval time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// Status reports a component's launchd job state (read-only `launchctl print`).
// A not-loaded job is reported as such (not an error); a genuine query failure
// errors.
func (m *Manager) Status(name string) (RunState, error) {
	out, err := m.Run("print", GUITarget(m.UID, name))
	if err != nil {
		if notLoaded(out) {
			return RunState{Loaded: false}, nil
		}
		return RunState{}, fmt.Errorf("print %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
	}
	return parsePrint(out), nil
}

// WritePlist renders + writes a component's plist with the exec indirection and
// the baked env. ProgramArguments launch SEXTANT ITSELF (Self) into
// `components exec <name>`, not the runtime binary — the env-resolution +
// re-exec happens in Go (components exec), solving launchd's minimal PATH.
func (m *Manager) WritePlist(name string, env Env) (string, error) {
	logPath := LogPath(name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}
	spec := plistSpec{
		Label:   Label(name),
		Program: []string{m.Self, "components", "exec", name},
		LogPath: logPath,
		Env:     env.Map(),
	}
	plist, err := genPlist(spec)
	if err != nil {
		return "", err
	}
	path := PlistPath(m.Home, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return "", fmt.Errorf("write plist %s: %w", path, err)
	}
	return path, nil
}

// bootstrapAndKickstart loads a plist into the gui domain and forces it to run.
// Reusing the #211 lesson: a bare bootstrap can leave a job loaded-but-runs=0,
// so we always kickstart after. A bootstrap that fails because the label is
// already loaded is benign (a restart re-bootstraps an existing plist); the
// kickstart still forces the relaunch.
func (m *Manager) bootstrapAndKickstart(name, plist string) error {
	if out, err := m.Run("bootstrap", guiDomain(m.UID), plist); err != nil {
		if !alreadyLoaded(out) {
			return fmt.Errorf("bootstrap %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
		}
	}
	if out, err := m.Run("kickstart", "-k", GUITarget(m.UID, name)); err != nil {
		return fmt.Errorf("kickstart %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
	}
	return nil
}

// bootout unloads a component's job. A not-loaded job is success (stopping an
// already-stopped component is not an error).
func (m *Manager) bootout(name string) error {
	out, err := m.Run("bootout", GUITarget(m.UID, name))
	if err != nil && !notLoaded(out) {
		return fmt.Errorf("bootout %s: %w (%s)", name, err, firstLine(strings.TrimSpace(out)))
	}
	return nil
}

// Install writes the plist (re-writing on a restart so an updated self-path or
// baked env is picked up), bootstraps + kickstarts, and HEALTH-CHECKS: it polls
// for a live process and warns LOUDLY with the log + the exact recovery if the
// job never runs, never reporting a hollow success. A bootout-before-write keeps
// a restart idempotent. stdout/stderr receive the operator-facing progress.
func (m *Manager) Install(stdout, stderr io.Writer, name string, env Env) error {
	_ = m.bootout(name) // idempotent rewrite
	path, err := m.WritePlist(name, env)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "  %s: wrote %s\n", name, path)

	if err := m.bootstrapAndKickstart(name, path); err != nil {
		return err
	}
	running := func() bool {
		st, err := m.Status(name)
		return err == nil && st.Running
	}
	if PollUntil(running, HealthBudget, PollInterval) {
		_, _ = fmt.Fprintf(stdout, "  %s: started (loaded + running)\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(stderr, "\n  WARNING: %s was loaded but did NOT come up running.\n", name)
	_, _ = fmt.Fprintf(stderr, "  Check its log: %s\n", LogPath(name))
	_, _ = fmt.Fprintf(stderr, "  Force a relaunch: launchctl kickstart -k %s\n", GUITarget(m.UID, name))
	return fmt.Errorf("%s did not reach running within %s", name, HealthBudget)
}

// Stop boots a component's job out of the gui domain. The plist stays on disk
// (a later start re-bootstraps it).
func (m *Manager) Stop(stdout io.Writer, name string) error {
	if err := m.bootout(name); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "  %s: stopped\n", name)
	return nil
}

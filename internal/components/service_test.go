package components

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeLaunchctl is the injected Runner for tests: it records invocations and
// returns scripted output/errors per verb, so a test drives bootstrap /
// kickstart / bootout / print without a real launchd. printOut can flip the
// reported state over poll calls (loaded→running).
type fakeLaunchctl struct {
	calls    [][]string
	printOut func(call int) (string, error)
	prints   int
	results  map[string]func(args []string) (string, error)
}

func (f *fakeLaunchctl) run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	if verb == "print" && f.printOut != nil {
		out, err := f.printOut(f.prints)
		f.prints++
		return out, err
	}
	if r, ok := f.results[verb]; ok {
		return r(args)
	}
	return "", nil
}

func (f *fakeLaunchctl) verbs() []string {
	var out []string
	for _, c := range f.calls {
		if len(c) > 0 {
			out = append(out, c[0])
		}
	}
	return out
}

func fastBudgets(t *testing.T) {
	t.Helper()
	hb, pi := HealthBudget, PollInterval
	HealthBudget, PollInterval = 30*time.Millisecond, time.Millisecond
	t.Cleanup(func() { HealthBudget, PollInterval = hb, pi })
}

// TestGenPlistExecIndirection: the rendered plist mirrors the bus plist's keys
// AND its ProgramArguments are the exec indirection — [<self>, components, exec,
// <name>], launching sextant itself, not the runtime binary — with the baked env.
func TestGenPlistExecIndirection(t *testing.T) {
	spec := plistSpec{
		Label:   Label("dispatcher"),
		Program: []string{"/opt/homebrew/bin/sextant", "components", "exec", "dispatcher"},
		LogPath: "/home/u/logs/dispatcher.log",
		Env:     map[string]string{"SEXTANT_MCP_BIN": "/opt/homebrew/bin/sextant-mcp", "PATH": "/x:/bin"},
	}
	out, err := genPlist(spec)
	if err != nil {
		t.Fatalf("genPlist: %v", err)
	}
	for _, want := range []string{
		"<string>dev.sextant.dispatcher</string>",
		"<key>KeepAlive</key>", "<key>RunAtLoad</key>",
		"<string>/opt/homebrew/bin/sextant</string>",
		"<string>components</string>", "<string>exec</string>", "<string>dispatcher</string>",
		"<key>EnvironmentVariables</key>",
		"<key>SEXTANT_MCP_BIN</key>", "<string>/opt/homebrew/bin/sextant-mcp</string>",
		"<key>StandardOutPath</key>", "<string>/home/u/logs/dispatcher.log</string>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	// It must NOT name the runtime binary directly (the whole point of the indirection).
	if strings.Contains(out, "sextant-dispatch") {
		t.Errorf("plist should re-exec sextant itself, not name the runtime binary directly:\n%s", out)
	}
}

// TestBootstrapAndKickstartHappy: a clean bootstrap is followed by a kickstart
// (the #211 lesson — never trust the bootstrap alone to have launched the job).
func TestBootstrapAndKickstartHappy(t *testing.T) {
	f := &fakeLaunchctl{}
	m := &Manager{UID: 501, Home: "/h", Self: "/b/sextant", Run: f.run}
	if err := m.bootstrapAndKickstart("dispatcher", "/p.plist"); err != nil {
		t.Fatalf("bootstrapAndKickstart: %v", err)
	}
	if v := f.verbs(); len(v) != 2 || v[0] != "bootstrap" || v[1] != "kickstart" {
		t.Fatalf("want bootstrap then kickstart, got %v", v)
	}
}

// TestBootstrapAlreadyLoadedIsBenign: a bootstrap that fails because the label
// is already loaded is not an error — the kickstart still forces the relaunch.
func TestBootstrapAlreadyLoadedIsBenign(t *testing.T) {
	f := &fakeLaunchctl{results: map[string]func([]string) (string, error){
		"bootstrap": func([]string) (string, error) {
			return "Bootstrap failed: 5: service already loaded", fmt.Errorf("exit 5")
		},
	}}
	m := &Manager{UID: 501, Run: f.run}
	if err := m.bootstrapAndKickstart("dispatcher", "/p.plist"); err != nil {
		t.Fatalf("already-loaded bootstrap should be benign, got %v", err)
	}
	if v := f.verbs(); len(v) != 2 || v[1] != "kickstart" {
		t.Fatalf("kickstart must still fire after a benign bootstrap; verbs=%v", v)
	}
}

// TestBootstrapRealFailure: a bootstrap error that is NOT already-loaded is
// surfaced loudly and the kickstart is not reached.
func TestBootstrapRealFailure(t *testing.T) {
	f := &fakeLaunchctl{results: map[string]func([]string) (string, error){
		"bootstrap": func([]string) (string, error) {
			return "permission denied", fmt.Errorf("exit 1")
		},
	}}
	m := &Manager{UID: 501, Run: f.run}
	if err := m.bootstrapAndKickstart("dispatcher", "/p.plist"); err == nil {
		t.Fatalf("a real bootstrap failure must error")
	}
	if v := f.verbs(); len(v) != 1 {
		t.Fatalf("kickstart must not run after a real bootstrap failure; verbs=%v", v)
	}
}

// TestBootoutNotLoadedIsSuccess: stopping a not-loaded job is not an error.
func TestBootoutNotLoadedIsSuccess(t *testing.T) {
	f := &fakeLaunchctl{results: map[string]func([]string) (string, error){
		"bootout": func([]string) (string, error) {
			return "Could not find service in domain", fmt.Errorf("exit 113")
		},
	}}
	m := &Manager{UID: 501, Run: f.run}
	if err := m.bootout("dispatcher"); err != nil {
		t.Fatalf("bootout of a not-loaded job should succeed, got %v", err)
	}
}

// TestStatusParsing: Status reports loaded+running from a print dump,
// not-loaded from "could not find", and surfaces a genuine query error.
func TestStatusParsing(t *testing.T) {
	running := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "dev.sextant.dispatcher = {\n\tstate = running\n\tpid = 4242\n}", nil
	}}
	m := &Manager{UID: 501, Run: running.run}
	st, err := m.Status("dispatcher")
	if err != nil || !st.Loaded || !st.Running {
		t.Fatalf("expected loaded+running; st=%+v err=%v", st, err)
	}

	notLoadedF := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "Could not find service \"x\" in domain", fmt.Errorf("exit 113")
	}}
	m = &Manager{UID: 501, Run: notLoadedF.run}
	st, err = m.Status("dispatcher")
	if err != nil || st.Loaded {
		t.Fatalf("not-loaded must not error and must report not loaded; st=%+v err=%v", st, err)
	}

	waiting := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "x = {\n\tstate = waiting\n}", nil
	}}
	m = &Manager{UID: 501, Run: waiting.run}
	st, _ = m.Status("dispatcher")
	if !st.Loaded || st.Running || st.Raw != "waiting" {
		t.Fatalf("expected loaded, not running, raw=waiting; st=%+v", st)
	}
}

// TestInstallHappy: a job that reaches running is written, bootstrapped,
// kickstarted, and health-checked — reporting started, no warning. The plist is
// on disk and carries the exec indirection.
func TestInstallHappy(t *testing.T) {
	fastBudgets(t)
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", t.TempDir())
	f := &fakeLaunchctl{printOut: func(int) (string, error) { return "x = {\n\tstate = running\n}", nil }}
	m := &Manager{UID: 501, Home: home, Self: "/opt/homebrew/bin/sextant", Run: f.run}

	var out, errOut strings.Builder
	env := Env{Path: "/x:/bin", McpBin: "/opt/homebrew/bin/sextant-mcp"}
	if err := m.Install(&out, &errOut, "dispatcher", env); err != nil {
		t.Fatalf("Install happy path: %v\nstderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "started (loaded + running)") {
		t.Fatalf("expected a started message; stdout=%q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("no warning on the happy path; stderr=%q", errOut.String())
	}
	plist, rerr := os.ReadFile(PlistPath(home, "dispatcher"))
	if rerr != nil {
		t.Fatalf("plist not written: %v", rerr)
	}
	if !strings.Contains(string(plist), "components") || !strings.Contains(string(plist), "/opt/homebrew/bin/sextant") {
		t.Fatalf("plist should carry the exec indirection; plist=%s", plist)
	}
	// bootout (idempotent) → bootstrap → kickstart → print(s).
	if v := f.verbs(); v[0] != "bootout" || v[1] != "bootstrap" || v[2] != "kickstart" {
		t.Fatalf("unexpected call order: %v", v)
	}
}

// TestInstallLoadedButNotRunning: the job loads but never runs (the
// post-bootstrap trap). Install must FAIL LOUD with the log + kickstart
// recovery, never a hollow success.
func TestInstallLoadedButNotRunning(t *testing.T) {
	fastBudgets(t)
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", t.TempDir())
	f := &fakeLaunchctl{printOut: func(int) (string, error) { return "x = {\n\tstate = waiting\n}", nil }}
	m := &Manager{UID: 501, Home: home, Self: "/b/sextant", Run: f.run}

	var out, errOut strings.Builder
	err := m.Install(&out, &errOut, "workflow", Env{Path: "/bin"})
	if err == nil {
		t.Fatalf("a never-running component must error")
	}
	es := errOut.String()
	if !strings.Contains(es, "did NOT come up running") || !strings.Contains(es, "kickstart -k") {
		t.Fatalf("expected a loud warning with the kickstart recovery; stderr=%q", es)
	}
	if strings.Contains(out.String(), "started (loaded + running)") {
		t.Fatalf("must not claim started when the job never ran; stdout=%q", out.String())
	}
}

// TestStop boots the job out and reports stopped.
func TestStop(t *testing.T) {
	f := &fakeLaunchctl{}
	m := &Manager{UID: 501, Run: f.run}
	var out strings.Builder
	if err := m.Stop(&out, "workflow"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if v := f.verbs(); len(v) != 1 || v[0] != "bootout" {
		t.Fatalf("stop should bootout once; verbs=%v", v)
	}
	if !strings.Contains(out.String(), "stopped") {
		t.Fatalf("expected a stopped message; stdout=%q", out.String())
	}
}

// TestPollUntil: comes up once cond is true; times out otherwise.
func TestPollUntil(t *testing.T) {
	n := 0
	if !PollUntil(func() bool { n++; return n >= 3 }, time.Second, time.Millisecond) {
		t.Fatalf("PollUntil should succeed once cond is true")
	}
	if PollUntil(func() bool { return false }, 20*time.Millisecond, time.Millisecond) {
		t.Fatalf("PollUntil should time out for a never-true cond")
	}
}

// TestGenPlistWellFormedXML: the rendered plist must be valid XML — the header
// must start with the literal "<?xml" (not "&lt;?xml") and the document must
// parse cleanly. This is the gate-gap that let the html/template bug ship: the
// earlier tests matched escaped fixtures and never validated plutil -lint.
//
// Failure mode before the fix: html/template HTML-escapes the XML declaration
// to "&lt;?xml ...", making every plist malformed; launchctl bootstrap fails
// with "Input/output error" and no component starts.
func TestGenPlistWellFormedXML(t *testing.T) {
	spec := plistSpec{
		Label:   Label("dispatcher"),
		Program: []string{"/opt/homebrew/bin/sextant", "components", "exec", "dispatcher"},
		LogPath: "/Users/u/Library/Logs/sextant/dispatcher.log",
		Env:     map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin"},
	}
	out, err := genPlist(spec)
	if err != nil {
		t.Fatalf("genPlist: %v", err)
	}

	// The XML declaration must be literal — not HTML-escaped.
	if !strings.HasPrefix(out, "<?xml") {
		pfx := out
		if len(pfx) > 64 {
			pfx = pfx[:64]
		}
		t.Errorf("plist does not start with literal <?xml — html/template escaping is corrupting the header\ngot prefix: %q", pfx)
	}

	// No HTML entity corruption anywhere in the rendered output.
	for _, bad := range []string{"&lt;", "&gt;", "&amp;"} {
		if strings.Contains(out, bad) {
			t.Errorf("plist contains HTML entity %q — text/template must be used, not html/template\n---\n%s", bad, out)
		}
	}

	// The full document must parse as valid XML.
	if xmlErr := xml.Unmarshal([]byte(out), new(any)); xmlErr != nil {
		t.Errorf("plist is not well-formed XML: %v\n---\n%s", xmlErr, out)
	}

	// On macOS, also validate with plutil -lint (the exact tool launchd uses).
	if runtime.GOOS == "darwin" {
		if _, lookErr := exec.LookPath("plutil"); lookErr == nil {
			f, tmpErr := os.CreateTemp("", "sextant-plist-*.plist")
			if tmpErr != nil {
				t.Fatalf("create temp plist: %v", tmpErr)
			}
			defer os.Remove(f.Name())
			if _, werr := f.WriteString(out); werr != nil {
				t.Fatalf("write temp plist: %v", werr)
			}
			f.Close()
			lintOut, lintErr := exec.Command("plutil", "-lint", f.Name()).CombinedOutput()
			if lintErr != nil {
				t.Errorf("plutil -lint failed: %v\n%s\n---plist:\n%s", lintErr, lintOut, out)
			}
		}
	}
}

// TestWriteRecipe materializes the embedded recipe under $SEXTANT_HOME.
func TestWriteRecipe(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	path, err := WriteRecipe()
	if err != nil {
		t.Fatalf("WriteRecipe: %v", err)
	}
	if path != RecipePath() {
		t.Fatalf("WriteRecipe path = %q, want %q", path, RecipePath())
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "claude") {
		t.Fatalf("recipe not written correctly: %v", err)
	}
}

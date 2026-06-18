package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeLaunchctl is the injected launchctlRunner for tests: it records the
// invocations and returns scripted output/errors per verb, so a test drives
// bootstrap / kickstart / bootout / print without a real launchd. printOut is a
// function so a test can flip the reported state over time (loaded→running).
type fakeLaunchctl struct {
	calls    [][]string
	printOut func(call int) (string, error) // per print call; call is the 0-based print index
	prints   int
	results  map[string]func(args []string) (string, error) // verb -> result
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

// TestGenPlistShape: the rendered plist mirrors the bus plist's keys —
// KeepAlive + RunAtLoad true, the program args, a combined log path, and the
// baked EnvironmentVariables (the launchd-PATH discover-then-bake).
func TestGenPlistShape(t *testing.T) {
	spec := plistSpec{
		Label:   "dev.sextant.dispatch",
		Program: []string{"/opt/homebrew/bin/sextant-dispatch", "--creds", "/c.creds", "--on-behalf"},
		LogPath: "/home/u/logs/dispatch.log",
		Env:     map[string]string{"SEXTANT_MCP_BIN": "/opt/homebrew/bin/sextant-mcp", "PATH": "/opt/homebrew/bin:/bin"},
	}
	out, err := genPlist(spec)
	if err != nil {
		t.Fatalf("genPlist: %v", err)
	}
	for _, want := range []string{
		"<key>Label</key>", "<string>dev.sextant.dispatch</string>",
		"<key>KeepAlive</key>", "<key>RunAtLoad</key>",
		"<string>/opt/homebrew/bin/sextant-dispatch</string>",
		"<string>--on-behalf</string>",
		"<key>StandardOutPath</key>", "<key>StandardErrorPath</key>",
		"<string>/home/u/logs/dispatch.log</string>",
		"<key>EnvironmentVariables</key>",
		"<key>SEXTANT_MCP_BIN</key>", "<string>/opt/homebrew/bin/sextant-mcp</string>",
		"<key>PATH</key>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
}

// TestBootstrapAndKickstartHappy: a clean bootstrap is followed by a kickstart —
// the #211 lesson (never trust the bootstrap alone to have launched the job).
func TestBootstrapAndKickstartHappy(t *testing.T) {
	f := &fakeLaunchctl{}
	if err := bootstrapAndKickstart(f.run, 501, "dispatch", "/p.plist"); err != nil {
		t.Fatalf("bootstrapAndKickstart: %v", err)
	}
	got := f.verbs()
	if len(got) != 2 || got[0] != "bootstrap" || got[1] != "kickstart" {
		t.Fatalf("want bootstrap then kickstart, got %v", got)
	}
}

// TestBootstrapAlreadyLoadedIsBenign: a bootstrap that fails because the label is
// already loaded is not an error — the kickstart still forces the relaunch (a
// restart re-bootstraps an existing plist).
func TestBootstrapAlreadyLoadedIsBenign(t *testing.T) {
	f := &fakeLaunchctl{results: map[string]func([]string) (string, error){
		"bootstrap": func([]string) (string, error) {
			return "Bootstrap failed: 5: Input/output error (service already loaded)", fmt.Errorf("exit 5")
		},
	}}
	if err := bootstrapAndKickstart(f.run, 501, "dispatch", "/p.plist"); err != nil {
		t.Fatalf("already-loaded bootstrap should be benign, got %v", err)
	}
	if v := f.verbs(); len(v) != 2 || v[1] != "kickstart" {
		t.Fatalf("kickstart must still fire after a benign bootstrap; verbs=%v", v)
	}
}

// TestBootstrapRealFailure: a bootstrap error that is NOT the already-loaded case
// is surfaced loudly and the kickstart is not reached.
func TestBootstrapRealFailure(t *testing.T) {
	f := &fakeLaunchctl{results: map[string]func([]string) (string, error){
		"bootstrap": func([]string) (string, error) {
			return "Could not bootstrap: permission denied", fmt.Errorf("exit 1")
		},
	}}
	err := bootstrapAndKickstart(f.run, 501, "dispatch", "/p.plist")
	if err == nil {
		t.Fatalf("a real bootstrap failure must error")
	}
	if v := f.verbs(); len(v) != 1 {
		t.Fatalf("kickstart must not run after a real bootstrap failure; verbs=%v", v)
	}
}

// TestBootoutNotLoadedIsSuccess: stopping a component whose job is not loaded is
// not an error.
func TestBootoutNotLoadedIsSuccess(t *testing.T) {
	f := &fakeLaunchctl{results: map[string]func([]string) (string, error){
		"bootout": func([]string) (string, error) {
			return "Could not find service in domain", fmt.Errorf("exit 113")
		},
	}}
	if err := bootout(f.run, 501, "dispatch"); err != nil {
		t.Fatalf("bootout of a not-loaded job should succeed, got %v", err)
	}
}

// TestPrintStateParsing: printState reports loaded+running from a real-ish print
// dump, not-loaded from "could not find", and surfaces a genuine query error.
func TestPrintStateParsing(t *testing.T) {
	running := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "dev.sextant.dispatch = {\n\tstate = running\n\tpid = 4242\n}", nil
	}}
	st, err := printState(running.run, 501, "dispatch")
	if err != nil || !st.Loaded || !st.Running {
		t.Fatalf("expected loaded+running; st=%+v err=%v", st, err)
	}

	notLoadedF := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "Could not find service \"dev.sextant.dispatch\" in domain", fmt.Errorf("exit 113")
	}}
	st, err = printState(notLoadedF.run, 501, "dispatch")
	if err != nil {
		t.Fatalf("not-loaded must not error: %v", err)
	}
	if st.Loaded {
		t.Fatalf("expected not loaded; st=%+v", st)
	}

	loadedNotRunning := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "dev.sextant.dispatch = {\n\tstate = waiting\n}", nil
	}}
	st, _ = printState(loadedNotRunning.run, 501, "dispatch")
	if !st.Loaded || st.Running || st.Raw != "waiting" {
		t.Fatalf("expected loaded, not running, raw=waiting; st=%+v", st)
	}
}

// TestPollRunningComesUp: the liveness poll catches the job once it reports
// running (the loaded-but-runs=0 → running transition).
func TestPollRunningComesUp(t *testing.T) {
	f := &fakeLaunchctl{printOut: func(call int) (string, error) {
		if call >= 2 { // not running for the first two polls, then up
			return "x = {\n\tstate = running\n}", nil
		}
		return "x = {\n\tstate = waiting\n}", nil
	}}
	if !pollRunning(f.run, 501, "dispatch", time.Second, time.Millisecond) {
		t.Fatalf("pollRunning should report up once the job runs")
	}
}

// TestPollRunningTimesOut: a job that never runs times out (false), so the caller
// can warn loudly rather than hang.
func TestPollRunningTimesOut(t *testing.T) {
	f := &fakeLaunchctl{printOut: func(int) (string, error) {
		return "x = {\n\tstate = waiting\n}", nil
	}}
	if pollRunning(f.run, 501, "dispatch", 20*time.Millisecond, time.Millisecond) {
		t.Fatalf("pollRunning should time out for a never-running job")
	}
}

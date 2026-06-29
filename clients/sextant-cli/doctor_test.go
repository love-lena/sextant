package main

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/bus/buscfg"
	"github.com/love-lena/sextant/clients/sextant-cli/internal/components"
	"github.com/love-lena/sextant/protocol/conninfo"
)

func TestRunDoctorMissingDiscovery(t *testing.T) {
	store := t.TempDir() // no bus.json
	var out strings.Builder
	runDoctor(&out, store)
	s := out.String()
	if !strings.Contains(s, "MISSING") {
		t.Errorf("doctor on a store with no bus.json should report MISSING discovery; got:\n%s", s)
	}
	// No recorded address ⇒ no reachability line (nothing to dial).
	if strings.Contains(s, "reachable:") {
		t.Errorf("doctor should not print a reachability line without a recorded URL; got:\n%s", s)
	}
}

func TestRunDoctorReachableAndPortHint(t *testing.T) {
	// A live listener stands in for the bus; bus.json points at it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	store := t.TempDir()
	url := "nats://" + ln.Addr().String()
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: url}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	var out strings.Builder
	runDoctor(&out, store)
	s := out.String()
	if !strings.Contains(s, "reachable: YES") {
		t.Errorf("doctor should report reachable YES for a live listener; got:\n%s", s)
	}
	// Unpinned port ⇒ the pin hint must show (the outage remedy).
	if !strings.Contains(s, "config set port") {
		t.Errorf("doctor should hint at pinning a port when unpinned; got:\n%s", s)
	}
}

func TestRunDoctorUnreachable(t *testing.T) {
	// Reserve then release a port so nothing is listening on the recorded address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	store := t.TempDir()
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: "nats://" + addr}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}
	if err := buscfg.Save(buscfg.Path(store), buscfg.Config{Port: 63527}); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	runDoctor(&out, store)
	s := out.String()
	if !strings.Contains(s, "reachable: NO") {
		t.Errorf("doctor should report reachable NO when nothing listens; got:\n%s", s)
	}
	// A pinned port should report as pinned (no hint).
	if !strings.Contains(s, "pinned") {
		t.Errorf("doctor should report a pinned port; got:\n%s", s)
	}
}

func TestExitCode(t *testing.T) {
	// A real non-zero exit yields its code; a non-exec error yields -1.
	err := exec.Command("sh", "-c", "exit 113").Run()
	if got := exitCode(err); got != 113 {
		t.Errorf("exitCode(exit 113) = %d, want 113", got)
	}
	if got := exitCode(fmt.Errorf("not an exec error")); got != -1 {
		t.Errorf("exitCode(plain err) = %d, want -1", got)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("one\ntwo\nthree"); got != "one" {
		t.Errorf("firstLine = %q, want \"one\"", got)
	}
	if got := firstLine("solo"); got != "solo" {
		t.Errorf("firstLine(no newline) = %q, want \"solo\"", got)
	}
}

func TestParseLaunchdState(t *testing.T) {
	// Trimmed real `launchctl print` output: the job's own top-level `state`
	// comes first; nested endpoint states (also "state = active") must not win.
	out := `gui/501/homebrew.mxcl.sextant = {
	active count = 1
	state = running
	stdout path = /opt/homebrew/var/log/sextant.log
	endpoints = {
		"x" = {
			state = active
		}
	}
}`
	state, logPath := parseLaunchdState(out)
	if state != "running" {
		t.Errorf("state = %q, want running", state)
	}
	if logPath != "/opt/homebrew/var/log/sextant.log" {
		t.Errorf("logPath = %q, want the sextant log", logPath)
	}

	// A not-running job (the throttle trap) must be detected as non-"running".
	notRunning := fmt.Sprintf("label = x\n\tstate = %s\n", "waiting")
	if st, _ := parseLaunchdState(notRunning); st != "waiting" {
		t.Errorf("state = %q, want waiting", st)
	}
}

// fakeRunner returns a components.Runner that always responds with the given
// output and error, regardless of the args — enough for the three component
// states doctor cares about: not-loaded, loaded+running, loaded-not-running.
func fakeRunner(out string, err error) components.Runner {
	return func(args ...string) (string, error) { return out, err }
}

// fakeLookPath returns a lookPath func that finds only the named binary. Anything
// else returns "not found" — lets tests control which components appear installed.
func fakeLookPath(found string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name == found {
			return "/fake/bin/" + name, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
}

// fakeLookPathAll returns a lookPath func that resolves every binary to a fake
// path — the "everything installed" case.
func fakeLookPathAll() func(string) (string, error) {
	return func(name string) (string, error) { return "/fake/bin/" + name, nil }
}

// TestDoctorComponentsMissing: when a binary is not on PATH, doctor reports
// MISSING and hints at installation.
func TestDoctorComponentsMissing(t *testing.T) {
	// Neither binary is found.
	lookPath := func(string) (string, error) { return "", fmt.Errorf("not found") }
	runner := fakeRunner("x = {\n\tstate = running\n}", nil)
	mgr := &components.Manager{UID: 501, Run: runner}
	var out strings.Builder
	for _, c := range components.Registry {
		reportDoctorComponent(&out, c, mgr, lookPath)
	}
	s := out.String()
	if !strings.Contains(s, "MISSING") {
		t.Errorf("missing binary should report MISSING; got:\n%s", s)
	}
	if !strings.Contains(s, "install sextant") {
		t.Errorf("missing binary should hint at installation; got:\n%s", s)
	}
}

// TestDoctorComponentsRunning: installed + launchd running → loaded + RUNNING.
func TestDoctorComponentsRunning(t *testing.T) {
	runner := fakeRunner("dev.sextant.dispatcher = {\n\tstate = running\n\tpid = 9999\n}", nil)
	mgr := &components.Manager{UID: 501, Run: runner}
	var out strings.Builder
	c, _ := components.Find("dispatcher")
	reportDoctorComponent(&out, c, mgr, fakeLookPathAll())
	s := out.String()
	if !strings.Contains(s, "loaded + RUNNING") {
		t.Errorf("running component should report loaded + RUNNING; got:\n%s", s)
	}
}

// TestDoctorComponentsNotLoaded: installed + service not loaded → NOT running +
// remediation hint pointing at `sextant components start`.
func TestDoctorComponentsNotLoaded(t *testing.T) {
	// launchctl exit 113 = not loaded.
	runner := fakeRunner("Could not find service in domain", fmt.Errorf("exit 113"))
	mgr := &components.Manager{UID: 501, Run: runner}
	var out strings.Builder
	c, _ := components.Find("workflow")
	reportDoctorComponent(&out, c, mgr, fakeLookPathAll())
	s := out.String()
	if !strings.Contains(s, "NOT running") {
		t.Errorf("not-loaded component should report NOT running; got:\n%s", s)
	}
	if !strings.Contains(s, "sextant components start") {
		t.Errorf("not-loaded component should hint at `sextant components start`; got:\n%s", s)
	}
}

// TestDoctorComponentsLoadedNotRunning: installed + loaded but NOT running (the
// throttle trap) → remediation hint pointing at `sextant components restart`.
func TestDoctorComponentsLoadedNotRunning(t *testing.T) {
	runner := fakeRunner("dev.sextant.dispatcher = {\n\tstate = waiting\n}", nil)
	mgr := &components.Manager{UID: 501, Run: runner}
	var out strings.Builder
	c, _ := components.Find("dispatcher")
	reportDoctorComponent(&out, c, mgr, fakeLookPathAll())
	s := out.String()
	if !strings.Contains(s, "loaded but NOT running") {
		t.Errorf("loaded-but-not-running should report loaded but NOT running; got:\n%s", s)
	}
	if !strings.Contains(s, "sextant components restart") {
		t.Errorf("loaded-but-not-running should hint at restart; got:\n%s", s)
	}
	// The dispatcher NeedsPi=true so the pi/node hint should appear.
	if !strings.Contains(s, "pi") || !strings.Contains(s, "node") {
		t.Errorf("dispatcher loaded-not-running should mention the pi/node hint; got:\n%s", s)
	}
}

// TestDoctorComponentsVioletNotLoadedHint: when violet is not loaded doctor
// surfaces the key-setup hint in addition to the start command.
func TestDoctorComponentsVioletNotLoadedHint(t *testing.T) {
	runner := fakeRunner("Could not find service in domain", fmt.Errorf("exit 113"))
	mgr := &components.Manager{UID: 501, Run: runner}
	var out strings.Builder
	c, ok := components.Find("violet")
	if !ok {
		t.Skip("violet not in Registry yet — skip")
	}
	reportDoctorComponent(&out, c, mgr, fakeLookPathAll())
	s := out.String()
	if !strings.Contains(s, "NOT running") {
		t.Errorf("not-loaded violet should report NOT running; got:\n%s", s)
	}
	if !strings.Contains(s, "sextant secret set anthropic") {
		t.Errorf("not-loaded violet should hint at `sextant secret set anthropic`; got:\n%s", s)
	}
}

// TestDoctorComponentsRunnerIntegration: runDoctorFull with an injected runner
// propagates component state into the full doctor output so the operator sees
// runtimes in one report.
func TestDoctorComponentsRunnerIntegration(t *testing.T) {
	store := t.TempDir() // missing bus.json is fine — we want the runtimes section
	runner := fakeRunner("dev.sextant.dispatcher = {\n\tstate = running\n\tpid = 1\n}", nil)
	var out strings.Builder
	runDoctorFull(&out, store, fakeLookPathAll(), runner)
	s := out.String()
	if !strings.Contains(s, "runtimes") {
		t.Errorf("doctor output should contain a runtimes section; got:\n%s", s)
	}
	if !strings.Contains(s, "dispatcher") {
		t.Errorf("doctor output should report the dispatcher component; got:\n%s", s)
	}
	if !strings.Contains(s, "workflow") {
		t.Errorf("doctor output should report the workflow component; got:\n%s", s)
	}
}

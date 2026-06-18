package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/clictx"
)

// TestRecipeDriftGuard keeps the embedded dispatcher recipe byte-for-byte equal
// to the source recipe in cmd/sextant-dispatch, so the source stays the single
// source of truth and a Homebrew install (binaries only) still ships an
// up-to-date harness.
func TestRecipeDriftGuard(t *testing.T) {
	embedded, err := embeddedRecipes.ReadFile("embed/agent.sh")
	if err != nil {
		t.Fatalf("read embedded recipe: %v", err)
	}
	source, err := os.ReadFile(filepath.Join("..", "sextant-dispatch", "recipes", "agent.sh"))
	if err != nil {
		t.Fatalf("read source recipe: %v", err)
	}
	if string(embedded) != string(source) {
		t.Fatalf("embedded recipe has drifted from cmd/sextant-dispatch/recipes/agent.sh — re-copy it into cmd/sextant/embed/agent.sh")
	}
}

// TestSelectComponents covers name resolution, --all, the both-error, and the
// require-one error for action commands.
func TestSelectComponents(t *testing.T) {
	if sel, err := selectComponents([]string{"dispatch"}, false, true); err != nil || len(sel) != 1 || sel[0].name != "dispatch" {
		t.Fatalf("name select: sel=%v err=%v", sel, err)
	}
	if sel, err := selectComponents(nil, true, true); err != nil || len(sel) != len(components) {
		t.Fatalf("--all select: sel=%d err=%v", len(sel), err)
	}
	if _, err := selectComponents([]string{"dispatch"}, true, true); err == nil {
		t.Fatalf("name + --all should error")
	}
	if _, err := selectComponents([]string{"nope"}, false, true); err == nil {
		t.Fatalf("unknown component should error")
	}
	if _, err := selectComponents(nil, false, true); err == nil {
		t.Fatalf("action with no name and no --all should error")
	}
	// status (requireOne=false) with no name reports all.
	if sel, err := selectComponents(nil, false, false); err != nil || len(sel) != len(components) {
		t.Fatalf("status no-name should report all; sel=%d err=%v", len(sel), err)
	}
}

// TestBakeEnv proves the launchd-PATH discover-then-bake: the baked PATH includes
// sextant's own dir plus the standard brew/system dirs, and a sibling sextant-mcp
// is discovered and baked into SEXTANT_MCP_BIN.
func TestBakeEnv(t *testing.T) {
	dir := t.TempDir()
	sextantBin := filepath.Join(dir, "sextant")
	mcpBin := filepath.Join(dir, "sextant-mcp")
	if err := os.WriteFile(sextantBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := bakeEnv(sextantBin)
	path := env["PATH"]
	if !strings.Contains(path, dir) {
		t.Fatalf("baked PATH should include sextant's own dir %q; got %q", dir, path)
	}
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		if !strings.Contains(path, want) {
			t.Errorf("baked PATH missing %q; got %q", want, path)
		}
	}
	if env["SEXTANT_MCP_BIN"] != mcpBin {
		t.Fatalf("a sibling sextant-mcp should be baked into SEXTANT_MCP_BIN; got %q want %q", env["SEXTANT_MCP_BIN"], mcpBin)
	}
}

// fastComponentBudgets shrinks the start health budget so the warn path completes
// in milliseconds.
func fastComponentBudgets(t *testing.T) {
	t.Helper()
	hb, pi := componentHealthBudget, componentPollInterval
	componentHealthBudget = 30 * time.Millisecond
	componentPollInterval = time.Millisecond
	t.Cleanup(func() { componentHealthBudget, componentPollInterval = hb, pi })
}

// seedComponentEnv points $SEXTANT_HOME at a temp dir and pre-seeds a component
// context so ensureIdentity reattaches (no real bus enrollment), and returns the
// temp HOME used for the LaunchAgents path and the on-PATH binary dir.
func seedComponentEnv(t *testing.T, c component) (home, binPath string) {
	t.Helper()
	cfgHome := t.TempDir()
	t.Setenv("SEXTANT_HOME", cfgHome)

	// Pre-seed the component's context + creds so ensureIdentity loads it.
	credsPath, err := clictx.WriteCreds("component-"+c.name, "FAKE-CREDS")
	if err != nil {
		t.Fatalf("seed creds: %v", err)
	}
	if err := clictx.Save(clictx.Context{
		Name: "component-" + c.name, ID: "01TESTID", Creds: credsPath, Kind: c.kind,
	}); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	// A fake binary on PATH so exec.LookPath(c.binary) resolves.
	binDir := t.TempDir()
	bp := filepath.Join(binDir, c.binary)
	if err := os.WriteFile(bp, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	return t.TempDir(), bp
}

// TestStartComponentHappy: a component whose launchd job reaches running is
// installed (plist written), bootstrapped, kickstarted, and health-checked —
// reporting started, no warning, with the discovered binary path baked in.
func TestStartComponentHappy(t *testing.T) {
	fastComponentBudgets(t)
	c, _ := findComponent("workflow") // no recipe needed; simplest happy path
	home, binPath := seedComponentEnv(t, c)

	f := &fakeLaunchctl{printOut: func(call int) (string, error) {
		return "x = {\n\tstate = running\n}", nil
	}}
	var out, errOut strings.Builder
	err := startComponent(&out, &errOut, c, home, 501, t.TempDir(), f.run)
	if err != nil {
		t.Fatalf("startComponent happy path errored: %v\nstderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "started (loaded + running)") {
		t.Fatalf("expected a started message; stdout=%q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("no warning expected on the happy path; stderr=%q", errOut.String())
	}
	// The plist exists and bakes the discovered binary path.
	plist, rerr := os.ReadFile(plistPath(home, c.name))
	if rerr != nil {
		t.Fatalf("plist not written: %v", rerr)
	}
	if !strings.Contains(string(plist), binPath) {
		t.Fatalf("plist should bake the discovered binary path %q", binPath)
	}
	// bootout (idempotent rewrite) → bootstrap → kickstart → print(s).
	if v := f.verbs(); v[0] != "bootout" || v[1] != "bootstrap" || v[2] != "kickstart" {
		t.Fatalf("unexpected launchctl call order: %v", v)
	}
}

// TestStartComponentLoadedButNotRunning: the job loads but never runs (the
// post-bootstrap trap). startComponent must FAIL LOUD — warning with the log
// path + kickstart recovery — never a hollow success.
func TestStartComponentLoadedButNotRunning(t *testing.T) {
	fastComponentBudgets(t)
	c, _ := findComponent("workflow")
	home, _ := seedComponentEnv(t, c)

	f := &fakeLaunchctl{printOut: func(call int) (string, error) {
		return "x = {\n\tstate = waiting\n}", nil // never running
	}}
	var out, errOut strings.Builder
	err := startComponent(&out, &errOut, c, home, 501, t.TempDir(), f.run)
	if err == nil {
		t.Fatalf("a never-running component must error")
	}
	es := errOut.String()
	if !strings.Contains(es, "did NOT come up running") {
		t.Fatalf("expected a loud warning; stderr=%q", es)
	}
	if !strings.Contains(es, "kickstart -k") {
		t.Fatalf("warning should name the kickstart recovery; stderr=%q", es)
	}
	if strings.Contains(out.String(), "started (loaded + running)") {
		t.Fatalf("must not claim started when the job never ran; stdout=%q", out.String())
	}
}

// TestStartComponentWritesRecipe: the dispatcher needs the embedded recipe on
// disk; starting it materializes the recipe and points --harness at it.
func TestStartComponentWritesRecipe(t *testing.T) {
	fastComponentBudgets(t)
	c, _ := findComponent("dispatch")
	home, _ := seedComponentEnv(t, c)

	f := &fakeLaunchctl{printOut: func(call int) (string, error) {
		return "x = {\n\tstate = running\n}", nil
	}}
	var out, errOut strings.Builder
	if err := startComponent(&out, &errOut, c, home, 501, t.TempDir(), f.run); err != nil {
		t.Fatalf("dispatch start errored: %v\nstderr=%s", err, errOut.String())
	}
	recipe := filepath.Join(clictx.Root(), "components", "agent.sh")
	if _, err := os.Stat(recipe); err != nil {
		t.Fatalf("recipe should be written to %s: %v", recipe, err)
	}
	plist, _ := os.ReadFile(plistPath(home, c.name))
	if !strings.Contains(string(plist), "sh "+recipe) {
		t.Fatalf("plist --harness should point at the written recipe; plist=%s", plist)
	}
	// The dispatcher mints with its own authority (no operator creds).
	if !strings.Contains(string(plist), "--on-behalf") {
		t.Fatalf("dispatch plist should use --on-behalf; plist=%s", plist)
	}
}

// TestStopComponent boots the job out and reports stopped.
func TestStopComponent(t *testing.T) {
	c, _ := findComponent("workflow")
	f := &fakeLaunchctl{}
	var out strings.Builder
	if err := stopComponent(&out, c, 501, f.run); err != nil {
		t.Fatalf("stopComponent: %v", err)
	}
	if v := f.verbs(); len(v) != 1 || v[0] != "bootout" {
		t.Fatalf("stop should bootout once; verbs=%v", v)
	}
	if !strings.Contains(out.String(), "stopped") {
		t.Fatalf("expected a stopped message; stdout=%q", out.String())
	}
}

// TestReportComponent renders the three states: missing binary, loaded+running,
// loaded-but-not-running.
func TestReportComponent(t *testing.T) {
	c, _ := findComponent("workflow")

	// Missing binary: PATH without the binary.
	t.Setenv("PATH", t.TempDir())
	var miss strings.Builder
	reportComponent(&miss, c, 501, (&fakeLaunchctl{}).run)
	if !strings.Contains(miss.String(), "MISSING") {
		t.Fatalf("missing binary should be reported; got %q", miss.String())
	}

	// Present binary + running job.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, c.binary), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	running := &fakeLaunchctl{printOut: func(int) (string, error) { return "x = {\n\tstate = running\n}", nil }}
	var run strings.Builder
	reportComponent(&run, c, 501, running.run)
	if !strings.Contains(run.String(), "RUNNING") {
		t.Fatalf("running job should be reported; got %q", run.String())
	}

	notRunning := &fakeLaunchctl{printOut: func(int) (string, error) { return "x = {\n\tstate = waiting\n}", nil }}
	var nr strings.Builder
	reportComponent(&nr, c, 501, notRunning.run)
	if !strings.Contains(nr.String(), "NOT running") {
		t.Fatalf("loaded-but-not-running should be reported; got %q", nr.String())
	}
}

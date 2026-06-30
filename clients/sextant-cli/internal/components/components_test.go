package components

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/protocol/wireapi"
)

// TestRecipeDriftGuard keeps the embedded dispatcher recipe byte-for-byte equal
// to the source recipe in clients/dispatcher/recipes, so the source stays the
// single source of truth and a Homebrew install (binaries only) still ships an
// up-to-date harness.
func TestRecipeDriftGuard(t *testing.T) {
	embedded, err := EmbeddedRecipe()
	if err != nil {
		t.Fatalf("read embedded recipe: %v", err)
	}
	// sextant-cli/internal/components -> repo root is four segments up, then to
	// the dispatcher's reference recipe.
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "clients", "dispatcher", "recipes", "pi.sh"))
	if err != nil {
		t.Fatalf("read source recipe: %v", err)
	}
	if string(embedded) != string(source) {
		t.Fatalf("embedded recipe has drifted from clients/dispatcher/recipes/pi.sh — re-copy it into the components embed dir")
	}
}

// TestEmbeddedPiBusBundle proves the pi-bus extension travels in the binary: the
// embedded bundle is present (built by scripts/build-pi-bus.sh), non-trivial, and
// is the ESM module pi loads — it carries the `sextantPiBus as default` export.
// A build that skipped the bundle step fails to COMPILE (the go:embed), so this
// guards that what shipped is actually the extension, not a stub.
func TestEmbeddedPiBusBundle(t *testing.T) {
	data, err := embedded.ReadFile("embed/pi-bus.bundle.mjs")
	if err != nil {
		t.Fatalf("read embedded pi-bus bundle: %v", err)
	}
	if len(data) < 1024 {
		t.Fatalf("pi-bus bundle suspiciously small (%d bytes) — esbuild bundle likely failed", len(data))
	}
	if !strings.Contains(string(data), "sextantPiBus as default") {
		t.Fatalf("pi-bus bundle missing the `sextantPiBus as default` export pi loads as the extension entry")
	}
}

// TestSelect covers name resolution, --all, the both-error, the require-one
// error for actions, and status's report-all default.
func TestSelect(t *testing.T) {
	if sel, err := Select("dispatcher", false, true); err != nil || len(sel) != 1 || sel[0].Name != "dispatcher" {
		t.Fatalf("name select: sel=%v err=%v", sel, err)
	}
	if sel, err := Select("", true, true); err != nil || len(sel) != len(Registry) {
		t.Fatalf("--all select: sel=%d err=%v", len(sel), err)
	}
	if _, err := Select("dispatcher", true, true); err == nil {
		t.Fatalf("name + --all should error")
	}
	if _, err := Select("nope", false, true); err == nil {
		t.Fatalf("unknown component should error")
	}
	if _, err := Select("", false, true); err == nil {
		t.Fatalf("action with no name and no --all should error")
	}
	if sel, err := Select("", false, false); err != nil || len(sel) != len(Registry) {
		t.Fatalf("status no-name should report all; sel=%d err=%v", len(sel), err)
	}
}

// TestDispatcherRegistryEntry pins the work-engine harness contract (ADR-0052):
// the dispatcher spawns headless pi workers, so it NeedsPi (fail loud if pi/node
// missing), NeedsKey (pi runs a real model), and NeedsRecipe (the pi.sh harness).
func TestDispatcherRegistryEntry(t *testing.T) {
	c, ok := Find("dispatcher")
	if !ok {
		t.Fatal("dispatcher is not registered")
	}
	if !c.NeedsPi {
		t.Error("dispatcher must NeedsPi — it spawns pi workers (the sole harness, ADR-0052)")
	}
	if !c.NeedsKey {
		t.Error("dispatcher must NeedsKey — pi runs a real model and needs ANTHROPIC_API_KEY")
	}
	if !c.NeedsRecipe {
		t.Error("dispatcher must NeedsRecipe — the embedded pi.sh harness")
	}
}

// TestDispatcherHarnessQuotesRecipePath guards the exit-127 bug the live rc
// caught: the dispatcher runs --harness via `sh -c`, and the macOS components dir
// lives under "Application Support" (a space), so the recipe path MUST be quoted
// or the inner sh word-splits it and the worker never launches.
func TestDispatcherHarnessQuotesRecipePath(t *testing.T) {
	c, _ := Find("dispatcher")
	spacey := "/Users/u/Library/Application Support/sextant/components/pi.sh"
	args := c.Args("/c/d.creds", "/s/store", spacey)
	i := slices.Index(args, "--harness")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("dispatcher Args missing --harness; got %v", args)
	}
	want := "sh '" + spacey + "'"
	if args[i+1] != want {
		t.Fatalf("harness must quote the recipe path so a space survives `sh -c`;\n got  %q\n want %q", args[i+1], want)
	}
}

// TestDashRegistryEntry pins AC#1/AC#3 of the managed-dash slice: the dash is a
// registered component (sextant-dash, kind=dash) whose Args carry --creds/--store,
// the managed $SEXTANT_HOME/dash.json state file, and --operator-session (so the
// page mints the OPERATOR's session, ADR-0047), with NO --port (the dash defaults
// to the stable 8765, AC#4). kind=dash is what makes the bus grant
// dashComponentPermissions + the delegated-mint capability. It also carries a
// HealthCheck (AC#2) and needs neither pi nor a key.
func TestDashRegistryEntry(t *testing.T) {
	c, ok := Find("dash")
	if !ok {
		t.Fatal("dash is not registered as a managed component")
	}
	if c.Binary != "sextant-dash" {
		t.Errorf("dash Binary = %q, want sextant-dash", c.Binary)
	}
	if c.Kind != wireapi.KindDash {
		t.Errorf("dash Kind = %q, want %q (so the bus mints dashComponentPermissions + the capability)", c.Kind, wireapi.KindDash)
	}
	if c.NeedsPi || c.NeedsKey || c.NeedsRecipe {
		t.Errorf("dash needs none of pi/key/recipe; got pi=%v key=%v recipe=%v", c.NeedsPi, c.NeedsKey, c.NeedsRecipe)
	}
	if c.HealthCheck == nil {
		t.Error("dash must carry a HealthCheck (AC#2: an HTTP-200 readiness probe, not just launchd running)")
	}

	args := c.Args("/c/dash.creds", "/s/store", "")
	wantPairs := map[string]string{
		"--creds":      "/c/dash.creds",
		"--store":      "/s/store",
		"--state-file": DashStateFile(),
	}
	for flag, want := range wantPairs {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) || args[i+1] != want {
			t.Errorf("dash Args missing %s %q; got %v", flag, want, args)
		}
	}
	if !slices.Contains(args, "--operator-session") {
		t.Errorf("dash Args must carry --operator-session (ADR-0047 delegated mint); got %v", args)
	}
	if slices.Contains(args, "--port") {
		t.Errorf("dash Args must NOT pass --port (the stable 8765 default, AC#4); got %v", args)
	}
}

// TestWorkflowComponentCarriesSaneStepTimeout is the AC#3 proof (the part verifiable
// without the live managed stack): the MANAGED workflow component's launch args carry a
// --step-timeout well above the coordinator binary's 90s default — so `sextant workflow
// start` drives a real coding step (minutes) to completion with NO operator flag. The
// fake-pass guard is explicit: a value <= 90s would mean the managed binary still gets the
// too-short default and the live scaffold's hand-run --step-timeout 30m is still required,
// which is exactly the bug TASK-257 fixes. (AC#1/#3's full live proof is a managed run
// completing a >90s step, gated on the assembled managed-path e2e — this asserts the
// component is CONFIGURED to make that pass.)
func TestWorkflowComponentCarriesSaneStepTimeout(t *testing.T) {
	c, ok := Find("workflow")
	if !ok {
		t.Fatal("workflow is not registered as a managed component")
	}
	args := c.Args("/c/workflow.creds", "/s/store", "")
	i := slices.Index(args, "--step-timeout")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("managed workflow Args must pass --step-timeout so the coordinator does not run at its 90s default (AC#3 no-manual-flags); got %v", args)
	}
	got, err := time.ParseDuration(args[i+1])
	if err != nil {
		t.Fatalf("--step-timeout %q is not a valid duration: %v", args[i+1], err)
	}
	// The coordinator binary's own default is 90s (clients/coordinator/main.go) — far too
	// short for a coding step. The managed value MUST exceed it, or the bug stands.
	if got <= 90*time.Second {
		t.Fatalf("managed workflow --step-timeout = %s; must exceed the coordinator's 90s default (a coding step runs minutes) — a value <= 90s is the TASK-257 bug, not the fix", got)
	}
}

// TestResolveEnvBakesPaths proves the launchd-PATH discover-then-bake: the
// composed PATH leads with pi's dir then carries node's dir and sextant-mcp's
// dir, then sextant's own dir + the system dirs; SEXTANT_MCP_BIN is the absolute
// sextant-mcp; and a needsPi dispatcher gets SEXTANT_PI_EXTENSION pointing at the
// deterministic materialized-bundle path.
func TestResolveEnvBakesPaths(t *testing.T) {
	look := func(bin string) (string, error) {
		switch bin {
		case "pi":
			return "/Users/u/.npm-global/bin/pi", nil
		case "node":
			return "/usr/local/bin/node", nil
		case "sextant-mcp":
			return "/opt/homebrew/bin/sextant-mcp", nil
		}
		return "", os.ErrNotExist
	}
	env, err := ResolveEnv("/opt/homebrew/bin/sextant", look, true)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	for _, want := range []string{
		"/Users/u/.npm-global/bin", "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin",
	} {
		if !strings.Contains(env.Path, want) {
			t.Errorf("PATH missing %q; got %q", want, env.Path)
		}
	}
	// pi's dir leads (it's off launchd's default PATH, so it must be present).
	if !strings.HasPrefix(env.Path, "/Users/u/.npm-global/bin:") {
		t.Errorf("pi's dir should lead the PATH; got %q", env.Path)
	}
	if env.McpBin != "/opt/homebrew/bin/sextant-mcp" {
		t.Fatalf("SEXTANT_MCP_BIN = %q, want the absolute sextant-mcp", env.McpBin)
	}
	if env.PiExtension != PiBusPath() {
		t.Fatalf("SEXTANT_PI_EXTENSION = %q, want the materialized-bundle path %q", env.PiExtension, PiBusPath())
	}
	if env.Map()["SEXTANT_PI_EXTENSION"] != PiBusPath() {
		t.Fatalf("Map() should carry SEXTANT_PI_EXTENSION = %q", PiBusPath())
	}
}

// TestResolveEnvFailLoudOnMissingPiOrNode: a dispatcher (needsPi) must fail loud
// when pi OR node is missing — never write a plist that cannot launch a worker.
// A non-pi component is unaffected by either being absent.
func TestResolveEnvFailLoudOnMissingPiOrNode(t *testing.T) {
	none := func(bin string) (string, error) { return "", os.ErrNotExist }
	if _, err := ResolveEnv("/b/sextant", none, true); err == nil {
		t.Fatalf("a needsPi component with no pi must fail loud")
	}
	// pi present but node missing must still fail loud (the recipe needs node).
	piNoNode := func(bin string) (string, error) {
		if bin == "pi" {
			return "/b/pi", nil
		}
		return "", os.ErrNotExist
	}
	if _, err := ResolveEnv("/b/sextant", piNoNode, true); err == nil {
		t.Fatalf("a needsPi component with pi but no node must fail loud")
	}
	// A non-pi component is fine without pi/node.
	if _, err := ResolveEnv("/b/sextant", none, false); err != nil {
		t.Fatalf("a component that does not need pi must not fail when pi/node are absent: %v", err)
	}
}

// TestWritePiBusMaterializes proves WritePiBus lands the embedded bundle at
// PiBusPath() (the path ResolveEnv bakes into SEXTANT_PI_EXTENSION), byte-equal
// to the embed — so the managed dispatcher loads exactly what shipped.
func TestWritePiBusMaterializes(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	path, err := WritePiBus()
	if err != nil {
		t.Fatalf("WritePiBus: %v", err)
	}
	if path != PiBusPath() {
		t.Fatalf("WritePiBus path = %q, want PiBusPath() %q", path, PiBusPath())
	}
	on, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read materialized bundle: %v", err)
	}
	want, _ := embedded.ReadFile("embed/pi-bus.bundle.mjs")
	if string(on) != string(want) {
		t.Fatalf("materialized bundle differs from the embedded bundle")
	}
}

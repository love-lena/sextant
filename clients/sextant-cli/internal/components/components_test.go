package components

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/love-lena/sextant/protocol/wireapi"
)

// TestRecipeDriftGuard keeps the embedded dispatcher recipe byte-for-byte equal
// to the source recipe in cmd/sextant-dispatch, so the source stays the single
// source of truth and a Homebrew install (binaries only) still ships an
// up-to-date harness.
func TestRecipeDriftGuard(t *testing.T) {
	embedded, err := EmbeddedRecipe()
	if err != nil {
		t.Fatalf("read embedded recipe: %v", err)
	}
	// sextant-cli/internal/components -> repo root is four segments up, then to
	// the dispatch app's reference recipe.
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "clients", "dispatcher", "recipes", "agent.sh"))
	if err != nil {
		t.Fatalf("read source recipe: %v", err)
	}
	if string(embedded) != string(source) {
		t.Fatalf("embedded recipe has drifted from clients/dispatcher/recipes/agent.sh — re-copy it into the components embed dir")
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

// TestDashRegistryEntry pins AC#1/AC#3 of the managed-dash slice: the dash is a
// registered component (sextant-dash, kind=dash) whose Args carry --creds/--store,
// the managed $SEXTANT_HOME/dash.json state file, and --operator-session (so the
// page mints the OPERATOR's session, ADR-0047), with NO --port (the dash defaults
// to the stable 8765, AC#4). kind=dash is what makes the bus grant
// dashComponentPermissions + the delegated-mint capability. It also carries a
// HealthCheck (AC#2) and needs neither claude nor a key.
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
	if c.NeedsClaude || c.NeedsKey || c.NeedsRecipe {
		t.Errorf("dash needs none of claude/key/recipe; got claude=%v key=%v recipe=%v", c.NeedsClaude, c.NeedsKey, c.NeedsRecipe)
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

// TestResolveEnvBakesPaths proves the launchd-PATH discover-then-bake: the
// composed PATH leads with claude's dir and sextant-mcp's dir, then sextant's
// own dir + the system dirs, and SEXTANT_MCP_BIN is the absolute sextant-mcp.
func TestResolveEnvBakesPaths(t *testing.T) {
	look := func(bin string) (string, error) {
		switch bin {
		case "claude":
			return "/Users/u/.local/bin/claude", nil
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
		"/Users/u/.local/bin", "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin",
	} {
		if !strings.Contains(env.Path, want) {
			t.Errorf("PATH missing %q; got %q", want, env.Path)
		}
	}
	// claude's dir leads (it's off launchd's default PATH, so it must be present).
	if !strings.HasPrefix(env.Path, "/Users/u/.local/bin:") {
		t.Errorf("claude's dir should lead the PATH; got %q", env.Path)
	}
	if env.McpBin != "/opt/homebrew/bin/sextant-mcp" {
		t.Fatalf("SEXTANT_MCP_BIN = %q, want the absolute sextant-mcp", env.McpBin)
	}
	if env.Map()["SEXTANT_MCP_BIN"] != "/opt/homebrew/bin/sextant-mcp" {
		t.Fatalf("Map() should carry SEXTANT_MCP_BIN")
	}
}

// TestResolveEnvFailLoudOnMissingClaude: a dispatcher (NeedsClaude) with no
// claude on PATH must error — never write a plist that cannot spawn.
func TestResolveEnvFailLoudOnMissingClaude(t *testing.T) {
	noClaude := func(bin string) (string, error) { return "", os.ErrNotExist }
	if _, err := ResolveEnv("/b/sextant", noClaude, true); err == nil {
		t.Fatalf("a NeedsClaude component with no claude must fail loud")
	}
	// A non-claude component is fine without claude.
	if _, err := ResolveEnv("/b/sextant", noClaude, false); err != nil {
		t.Fatalf("a component that does not need claude must not fail when claude is absent: %v", err)
	}
}

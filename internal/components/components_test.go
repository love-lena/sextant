package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// internal/components -> ../../cmd/sextant-dispatch/recipes/agent.sh
	source, err := os.ReadFile(filepath.Join("..", "..", "cmd", "sextant-dispatch", "recipes", "agent.sh"))
	if err != nil {
		t.Fatalf("read source recipe: %v", err)
	}
	if string(embedded) != string(source) {
		t.Fatalf("embedded recipe has drifted from cmd/sextant-dispatch/recipes/agent.sh — re-copy it into internal/components/embed/agent.sh")
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

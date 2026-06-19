package components

import (
	"os"
	"os/exec"
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

// TestDispatcherHarnessQuotesSpacePath asserts that the dispatcher's built
// --harness arg correctly quotes a recipe path that contains a space (e.g.
// the standard macOS "$HOME/Library/Application Support/sextant/components/agent.sh").
// Before the shellQuote fix, "sh " + recipe produced a string that sh -c would
// split at the space, yielding exit 127 on macOS. After the fix the harness
// string must be parseable by `sh -c` as a single file path.
func TestDispatcherHarnessQuotesSpacePath(t *testing.T) {
	const spacePath = "/tmp/has space/agent.sh"

	dispatcher, ok := Find("dispatcher")
	if !ok {
		t.Fatal("dispatcher not in Registry")
	}
	args := dispatcher.Args("fake.creds", "/tmp/store", spacePath)

	// locate the --harness value
	harness := ""
	for i, a := range args {
		if a == "--harness" && i+1 < len(args) {
			harness = args[i+1]
			break
		}
	}
	if harness == "" {
		t.Fatalf("--harness not found in dispatcher args: %v", args)
	}

	// The harness must contain the single-quoted form of the path, not the
	// bare unquoted form that splits on the space.
	want := "'" + spacePath + "'"
	if !strings.Contains(harness, want) {
		t.Errorf("harness %q does not contain quoted path %q — paths with spaces will split under sh -c", harness, want)
	}
	// Sanity: it must NOT contain the bare unquoted path with a leading space
	// before the first slash (that is the broken "sh /tmp/has" split form).
	if strings.Contains(harness, " /tmp/has space") {
		t.Errorf("harness %q contains unquoted space path — still broken", harness)
	}

	// Integration: write a stub recipe to a path with a space and confirm
	// `sh -c <harness>` actually resolves to the right script (it echos a
	// sentinel and exits 0). This catches any future regression where the
	// quoting is syntactically present but semantically wrong.
	dir := t.TempDir()
	recipePath := filepath.Join(dir, "has space", "agent.sh")
	if err := os.MkdirAll(filepath.Dir(recipePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sentinel := "HARNESS_OK"
	if err := os.WriteFile(recipePath, []byte("#!/bin/sh\necho "+sentinel+"\n"), 0o755); err != nil {
		t.Fatalf("write stub recipe: %v", err)
	}

	args2 := dispatcher.Args("fake.creds", "/tmp/store", recipePath)
	harness2 := ""
	for i, a := range args2 {
		if a == "--harness" && i+1 < len(args2) {
			harness2 = args2[i+1]
			break
		}
	}

	out, err := exec.Command("sh", "-c", harness2).Output()
	if err != nil {
		t.Fatalf("sh -c %q failed: %v (exit 127 = unquoted path split)", harness2, err)
	}
	if !strings.Contains(string(out), sentinel) {
		t.Errorf("sh -c %q: got %q, want sentinel %q", harness2, string(out), sentinel)
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

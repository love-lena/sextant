package components

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// env.go resolves the environment a managed runtime needs — the launchd-PATH
// discover-then-bake. It is pure and testable: the discovery takes injected
// lookers so a test drives the present/missing paths without a real claude or
// sextant-mcp on disk, and the production callers pass exec.LookPath + os.Stat.

//go:embed embed/agent.sh
var embedded embed.FS

// Env is the resolved environment for a component: the composed PATH and the
// absolute SEXTANT_MCP_BIN. Both are baked into the plist's
// EnvironmentVariables AND applied by `components exec` before the re-exec, so
// launchd and the re-exec'd runtime see the same values.
type Env struct {
	Path   string // composed PATH covering claude, sextant-mcp, brew, and system bins
	McpBin string // absolute sextant-mcp path (empty if not found)
}

// Map renders the Env as the plist EnvironmentVariables map.
func (e Env) Map() map[string]string {
	m := map[string]string{"PATH": e.Path}
	if e.McpBin != "" {
		m["SEXTANT_MCP_BIN"] = e.McpBin
	}
	return m
}

// looker resolves a binary to its absolute path (exec.LookPath in production).
type looker func(string) (string, error)

// ResolveEnv composes the launchd PATH and SEXTANT_MCP_BIN at `components start`
// time, where the real interactive PATH still exists. launchd's own PATH is
// minimal, so we DISCOVER (LookPath claude, LookPath sextant-mcp) then BAKE the
// covering dirs:
//
//	dirname(claude) : dirname(sextant-mcp) : dirname(self) : /opt/homebrew/bin :
//	/usr/local/bin : /usr/bin : /bin
//
// selfBin is the running sextant binary's path (its dir holds the sibling
// runtime binaries on a brew install). look is exec.LookPath. needsClaude makes
// a missing claude a HARD error — never write a plist for a dispatcher that
// cannot spawn (claude lives at ~/.local/bin/claude, off launchd's PATH).
func ResolveEnv(selfBin string, look looker, needsClaude bool) (Env, error) {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}

	claude, claudeErr := look("claude")
	if claudeErr != nil && needsClaude {
		return Env{}, fmt.Errorf("`claude` not found on PATH — the dispatcher's spawn recipe needs it; "+
			"install the Claude CLI (commonly ~/.local/bin/claude) and ensure it is on your PATH, then retry (%w)", claudeErr)
	}
	if claudeErr == nil {
		add(filepath.Dir(claude))
	}

	mcp, mcpErr := look("sextant-mcp")
	if mcpErr == nil {
		add(filepath.Dir(mcp))
	}
	add(filepath.Dir(selfBin)) // sibling runtime binaries live beside sextant
	add("/opt/homebrew/bin")
	add("/usr/local/bin")
	add("/usr/bin")
	add("/bin")

	env := Env{Path: strings.Join(dirs, ":")}
	if mcpErr == nil {
		env.McpBin = mcp
	}
	return env, nil
}

// WriteRecipe materializes the embedded dispatcher recipe to RecipePath() and
// returns it. A Homebrew install ships only the binaries, so the recipe the
// dispatcher's --harness needs must come from the embed. It is rewritten each
// start so an upgraded recipe is picked up. components_test.go's drift guard
// keeps the embedded copy identical to cmd/sextant-dispatch/recipes/agent.sh.
func WriteRecipe() (string, error) {
	data, err := embedded.ReadFile("embed/agent.sh")
	if err != nil {
		return "", fmt.Errorf("read embedded recipe: %w", err)
	}
	if err := os.MkdirAll(componentsDir(), 0o755); err != nil {
		return "", fmt.Errorf("create components dir: %w", err)
	}
	path := RecipePath()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", fmt.Errorf("write recipe %s: %w", path, err)
	}
	return path, nil
}

// EmbeddedRecipe returns the embedded recipe bytes (for the drift-guard test).
func EmbeddedRecipe() ([]byte, error) { return embedded.ReadFile("embed/agent.sh") }

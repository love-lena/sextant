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
// lookers so a test drives the present/missing paths without a real pi or
// sextant-mcp on disk, and the production callers pass exec.LookPath + os.Stat.

// The embed FS carries the two on-disk assets a managed dispatcher needs, both
// shipped INSIDE the sextant binary because a Homebrew install lays down only
// binaries: the pi recipe (the --harness the dispatcher shells out to) and the
// pi-bus extension bundle (the --extension that pi loads). WriteRecipe /
// WritePiBus materialize them beside the component's creds at start.
//
// pi-bus.bundle.mjs is GENERATED (esbuild, scripts/build-pi-bus.sh) and
// gitignored, like the dash UI bundles (TASK-121): naming it in the embed makes
// a build that skipped the bundle step fail to COMPILE rather than ship a
// dispatcher that cannot launch a worker. `make build`/`test`, CI, and
// scripts/release.sh all run the bundle step before any Go compile.
//
//go:generate bash ../../../../scripts/build-pi-bus.sh
//go:embed embed/pi.sh embed/pi-bus.bundle.mjs
var embedded embed.FS

// Env is the resolved environment for a component: the composed PATH, the
// absolute SEXTANT_MCP_BIN, and (for the dispatcher) the absolute
// SEXTANT_PI_EXTENSION. All are baked into the plist's EnvironmentVariables AND
// applied by `components exec` before the re-exec, so launchd and the re-exec'd
// runtime see the same values.
type Env struct {
	Path        string // composed PATH covering pi, node, sextant-mcp, brew, and system bins
	McpBin      string // absolute sextant-mcp path (empty if not found)
	PiExtension string // absolute pi-bus extension bundle path (dispatcher only; empty otherwise)
}

// Map renders the Env as the plist EnvironmentVariables map.
func (e Env) Map() map[string]string {
	m := map[string]string{"PATH": e.Path}
	if e.McpBin != "" {
		m["SEXTANT_MCP_BIN"] = e.McpBin
	}
	if e.PiExtension != "" {
		m["SEXTANT_PI_EXTENSION"] = e.PiExtension
	}
	return m
}

// looker resolves a binary to its absolute path (exec.LookPath in production).
type looker func(string) (string, error)

// ResolveEnv composes the launchd PATH, SEXTANT_MCP_BIN, and (for a needsPi
// dispatcher) SEXTANT_PI_EXTENSION at `components start` time, where the real
// interactive PATH still exists. launchd's own PATH is minimal, so we DISCOVER
// (LookPath pi, node, sextant-mcp) then BAKE the covering dirs:
//
//	dirname(pi) : dirname(node) : dirname(sextant-mcp) : dirname(self) :
//	/opt/homebrew/bin : /usr/local/bin : /usr/bin : /bin
//
// selfBin is the running sextant binary's path (its dir holds the sibling
// runtime binaries on a brew install). look is exec.LookPath. needsPi makes a
// missing pi OR node a HARD error — never write a plist for a dispatcher that
// cannot launch its headless pi worker. pi and node commonly live off launchd's
// minimal PATH (a brew or npm-global bin dir), so we discover and bake them.
func ResolveEnv(selfBin string, look looker, needsPi bool) (Env, error) {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}

	if needsPi {
		pi, err := look("pi")
		if err != nil {
			return Env{}, fmt.Errorf("`pi` not found on PATH — the dispatcher spawns headless pi workers (the work engine's harness); "+
				"install pi (`npm install -g @earendil-works/pi-coding-agent`) and ensure it is on your PATH, then retry (%w)", err)
		}
		add(filepath.Dir(pi))
		// The pi recipe shells out to `node` (it JSON-encodes the first prompt) and
		// pi itself is a Node program, so node is a hard requirement too.
		node, err := look("node")
		if err != nil {
			return Env{}, fmt.Errorf("`node` not found on PATH — the dispatcher's pi recipe needs Node (>=22) to run the pi-bus extension; "+
				"install Node and ensure it is on your PATH, then retry (%w)", err)
		}
		add(filepath.Dir(node))
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
	if needsPi {
		// The bundle path is DETERMINISTIC (no I/O here): startComponent /
		// componentsExec materialize the embedded bundle to exactly this path with
		// WritePiBus, so the baked-at-install env and the applied-at-exec env agree.
		env.PiExtension = PiBusPath()
	}
	return env, nil
}

// WriteRecipe materializes the embedded dispatcher recipe (pi.sh) to RecipePath()
// and returns it. A Homebrew install ships only the binaries, so the recipe the
// dispatcher's --harness needs must come from the embed. It is rewritten each
// start so an upgraded recipe is picked up. components_test.go's drift guard
// keeps the embedded copy identical to clients/dispatcher/recipes/pi.sh.
func WriteRecipe() (string, error) {
	data, err := embedded.ReadFile("embed/pi.sh")
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
func EmbeddedRecipe() ([]byte, error) { return embedded.ReadFile("embed/pi.sh") }

// WritePiBus materializes the embedded pi-bus extension bundle to PiBusPath()
// and returns it — the deterministic path ResolveEnv bakes into
// SEXTANT_PI_EXTENSION. Like WriteRecipe it is rewritten each start (so an
// upgraded bundle is picked up) and shipped in the binary because a brew install
// lays down no node_modules: the bundle is a single self-contained ESM file
// (sdk + conventions + typebox inlined; pi provides the pi host).
func WritePiBus() (string, error) {
	data, err := embedded.ReadFile("embed/pi-bus.bundle.mjs")
	if err != nil {
		return "", fmt.Errorf("read embedded pi-bus extension: %w", err)
	}
	if err := os.MkdirAll(componentsDir(), 0o755); err != nil {
		return "", fmt.Errorf("create components dir: %w", err)
	}
	path := PiBusPath()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write pi-bus extension %s: %w", path, err)
	}
	return path, nil
}

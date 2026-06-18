// Package components manages the agent RUNTIMES — the dispatcher and the
// workflow coordinator — as keep-alive, OS-managed background services, so an
// operator never has to hunt a pid (the "never hunt a pid" goal, v0.5.3 S5).
//
// Homebrew allows exactly one service per formula and the bus already is it
// (homebrew.mxcl.sextant runs `sextant up`), so the bus stays the brew service
// and is NOT managed here. `sextant components` stands up the OTHER components
// itself: a per-component LaunchAgent under ~/Library/LaunchAgents/ (label
// dev.sextant.<name>), bootstrapped + kickstarted into the user's gui domain.
//
// THE EXEC INDIRECTION (the keystone). A component's plist does NOT run the
// runtime binary directly. Its ProgramArguments are
//
//	[<the sextant binary>, components, exec, <name>]
//
// so launchd launches SEXTANT ITSELF, which resolves the environment in Go and
// syscall.Execs the real sextant-<name>. This solves launchd's minimal-PATH
// problem (the dispatcher's recipe shells out to `claude`, which is not on
// launchd's default PATH) in ONE testable Go function rather than a
// plist-embedded shell — and it is the same seam a later env-file component
// (violet) reuses. The resolved PATH + SEXTANT_MCP_BIN are ALSO baked into the
// plist's EnvironmentVariables, so the values are visible to launchd and to the
// re-exec'd binary alike.
//
// macOS only: launchctl is the service plane (launchd_darwin.go). On other OSes
// the launchd entry points return a clear "managed services are macOS-only"
// error (launchd_other.go); there is no systemd path in v0.5.3.
package components

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/love-lena/sextant/internal/clictx"
)

// Component is one managed runtime: the service name, the real binary it
// re-execs into, the bus kind it enrolls as, and how its launch args are built.
type Component struct {
	// Name is the service name + creds/context handle (e.g. "dispatcher").
	Name string
	// Binary is the runtime binary basename re-exec'd by `components exec`
	// (e.g. "sextant-dispatch").
	Binary string
	// Kind is the bus kind the component's identity is minted as.
	Kind string
	// NeedsClaude is true when the component's runtime spawns `claude` (the
	// dispatcher), so `components start` fails loud if claude is not found —
	// never writing a plist that silently cannot spawn.
	NeedsClaude bool
	// NeedsKey is true when the component's runtime needs an Anthropic API key
	// (violet's model turns). The key is read from the 0600 VioletEnvPath() at
	// exec time and set in the environment before the re-exec — NEVER baked into
	// the world-readable plist. `components start` fails loud if the env-file is
	// absent or carries no key, never starting the component keyless.
	NeedsKey bool
	// Args builds the runtime's flags after the binary. creds is the component's
	// own creds file (passed --creds explicitly, never the active context); store
	// is the bus store dir; recipe is the on-disk dispatcher recipe ("" when the
	// component needs none).
	Args func(creds, store, recipe string) []string
	// NeedsRecipe is true when the component needs the embedded dispatcher recipe
	// materialized on disk (the dispatcher's --harness).
	NeedsRecipe bool
}

// Registry is the set of managed runtimes: the dispatcher, the workflow
// coordinator, and violet (the operator's assistant). The dash is deliberately
// NOT here — it is the operator's foreground surface, not a keep-alive runtime.
var Registry = []Component{
	{
		Name:        "dispatcher",
		Binary:      "sextant-dispatch",
		Kind:        "dispatcher",
		NeedsClaude: true,
		NeedsRecipe: true,
		Args: func(creds, store, recipe string) []string {
			// --on-behalf: the dispatcher mints children with its OWN authority
			// (ADR-0033), so the launchd service runs unattended with no operator
			// credential. The harness is the embedded recipe written to disk.
			return []string{
				"--creds", creds, "--store", store,
				"--on-behalf", "--harness", "sh " + recipe,
			}
		},
	},
	{
		Name:   "workflow",
		Binary: "sextant-workflow",
		Kind:   "workflow",
		Args: func(creds, store, recipe string) []string {
			// Listen mode (no --plan/--id): subscribe to workflow.start and run one
			// coordinator per request (the dash's "start a workflow" path).
			return []string{"--creds", creds, "--store", store}
		},
	},
	{
		Name:     "violet",
		Binary:   "sextant-violet",
		Kind:     "agent",
		NeedsKey: true,
		Args: func(creds, store, recipe string) []string {
			// --designate: on every start violet (re-)points the `assistant`
			// designation artifact (ADR-0039) at itself — idempotent, so the live
			// assistant is whichever violet client is currently up. The key is set
			// in the environment by `components exec` (from VioletEnvPath()), and
			// sextant-violet's --api-key defaults from $ANTHROPIC_API_KEY, so no
			// secret rides in these args.
			return []string{"--creds", creds, "--store", store, "--designate"}
		},
	},
}

// Find returns the registered component by name, or false.
func Find(name string) (Component, bool) {
	for _, c := range Registry {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// Names returns the registered component names, sorted, for error messages.
func Names() []string {
	out := make([]string, 0, len(Registry))
	for _, c := range Registry {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// Select resolves a name / --all into the components to act on. An action
// (requireOne) with neither a name nor --all is an error — an explicit choice
// avoids surprising the operator by acting on everything by default. Status
// (requireOne=false) with no name reports all.
func Select(name string, all, requireOne bool) ([]Component, error) {
	switch {
	case all && name != "":
		return nil, fmt.Errorf("pass a name OR --all, not both")
	case all:
		return Registry, nil
	case name != "":
		c, ok := Find(name)
		if !ok {
			return nil, fmt.Errorf("unknown component %q (known: %v)", name, Names())
		}
		return []Component{c}, nil
	case requireOne:
		return nil, fmt.Errorf("name a component or pass --all (known: %v)", Names())
	default:
		return Registry, nil
	}
}

// componentsDir is where per-component state lives, under the client-config root
// ($SEXTANT_HOME): creds, the materialized recipe, logs. It sits beside the
// context store and needs no brew var dir (a non-brew install lacks one).
func componentsDir() string { return filepath.Join(clictx.Root(), "components") }

// CredsPath is a component's own creds file. The plist passes this with --creds
// EXPLICITLY so the service connects as itself, never via the operator's active
// context (which the operator's CLI mutates).
func CredsPath(name string) string { return filepath.Join(componentsDir(), name+".creds") }

// RecipePath is where the embedded dispatcher recipe is materialized.
func RecipePath() string { return filepath.Join(componentsDir(), "agent.sh") }

// LogPath is a component's combined stdout+stderr log.
func LogPath(name string) string { return filepath.Join(clictx.Root(), "logs", name+".log") }

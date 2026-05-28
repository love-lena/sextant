package component

import (
	"fmt"
	"sync"
)

// Meta describes a registered Tier 1 component for discovery surfaces
// (the `sextant tui` menu, the `sextant dash` multipane). Populated by
// each component package's init() so the registry is built at process
// boot without anyone walking the package tree.
//
// Field semantics:
//
//   - Name        — short stable identifier ("agents-list", "chat",
//     "pending-list"). Used as the registry's primary key; double-
//     registration with the same Name panics in Register.
//   - Description — one-line summary the Huh menu / dash launcher
//     surfaces next to the entry.
//   - Command     — cobra command path the discovery menu maps the
//     entry to ("agents list", "agents chat", "pending list"). The
//     `sextant tui` menu uses this to spawn the equivalent `-i`
//     invocation; the dash uses Name + New directly.
//   - New         — factory the host invokes to mount the component.
//     Returning a fresh *Model per call lets the dash mount the same
//     surface multiple times without state aliasing.
type Meta struct {
	Name        string
	Description string
	Command     string
	New         func() Component
}

// registry guards the global slice. Registration happens once per
// process at init() time, but List() is allowed to race with it from
// tests that register from a TestMain — the mutex keeps both sides
// safe without forcing a init-ordering rule.
var (
	registry   []Meta
	registryMu sync.RWMutex
)

// Register adds m to the global registry. Intended to be called from
// each component package's init(). Panics if a Meta with the same
// Name is already registered — catches accidental double-registration
// at boot, which would otherwise silently land the second factory and
// leak the first.
//
// Registration order is preserved by List(): the first init() to call
// Register lands at index 0, the second at index 1, etc. Go runs
// package init()s in dependency order, then alphabetical within a
// dependency level — the ordering is therefore deterministic across
// builds.
func Register(m Meta) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, existing := range registry {
		if existing.Name == m.Name {
			panic(fmt.Sprintf("component: duplicate registration for %q", m.Name))
		}
	}
	registry = append(registry, m)
}

// List returns all registered components in registration order. The
// returned slice is a copy — callers can sort or filter it without
// disturbing the registry.
func List() []Meta {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Meta, len(registry))
	copy(out, registry)
	return out
}

// resetRegistryForTest empties the registry. Exposed via the
// registry_test.go file's same-package access; not exported because
// production code should never reset.
func resetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = nil
}

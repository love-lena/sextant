// Package containermgr is sextantd's wrapper around the Docker SDK for
// agent-incarnation containers. It handles socket discovery (OrbStack on
// macOS, Docker Desktop / dockerd everywhere else), run/stop/inspect/list
// with sextant-shaped argument structs, and forced cleanup of containers
// that match a label filter (the M11 acceptance test relies on this).
//
// The package is intentionally small: it exposes one Manager type and a
// few argument structs. The spawn handler (pkg/rpc/handlers/spawn.go)
// is the only production caller; tests in pkg/containermgr exercise the
// run/stop round-trip against a real Docker daemon.
//
// Plan: plans/bootstrap.md#M11
// Spec: specs/components/sextantd.md §"Container management"
package containermgr

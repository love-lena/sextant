package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// HostDep describes one external-binary preflight check.
type HostDep struct {
	Name        string // binary name to look up on PATH
	Kind        string // CheckResult.Kind value (always "host-dep")
	Contributor bool   // true → only checked when --contributor is set
}

// hostDeps is the canonical list of binaries sextant needs on PATH.
// Operator-required (Contributor=false) deps must be present for a
// running install to function. Contributor deps are needed only to
// build / develop sextant itself.
var hostDeps = []HostDep{
	{Name: "nats-server", Kind: "host-dep"},
	{Name: "clickhouse", Kind: "host-dep"},
	{Name: "docker", Kind: "host-dep"},
	{Name: "go", Kind: "host-dep", Contributor: true},
	{Name: "node", Kind: "host-dep", Contributor: true},
	{Name: "npm", Kind: "host-dep", Contributor: true},
}

// lookPathFn is exec.LookPath, indirected so tests can inject a fake.
type lookPathFn func(string) (string, error)

// dockerInfoFn runs `docker info` and reports whether the daemon is
// reachable. Indirected so tests can simulate "binary present but
// daemon down".
type dockerInfoFn func(context.Context) error

// installHint returns the platform-specific remedy string for a missing
// binary. Falls back to a generic "install <name>" when the platform is
// unrecognized.
func installHint(name string) string {
	switch runtime.GOOS {
	case "darwin":
		switch name {
		case "docker":
			return "install OrbStack: brew install --cask orbstack"
		case "nats-server":
			return "brew install nats-server"
		case "clickhouse":
			return "brew install clickhouse"
		case "go":
			return "brew install go  # requires >= 1.26"
		case "node":
			return "brew install node"
		case "npm":
			return "brew install node  # bundles npm"
		}
	case "linux":
		switch name {
		case "docker":
			return "apt install docker.io  (or install OrbStack)"
		case "nats-server":
			return "download from https://github.com/nats-io/nats-server/releases"
		case "clickhouse":
			return "see https://clickhouse.com/docs/en/install"
		case "go":
			return "see https://go.dev/dl  (>= 1.26)"
		case "node", "npm":
			return "apt install nodejs npm"
		}
	}
	return "install " + name
}

// checkHostBinary verifies that name is on PATH via lookFn. Pass
// exec.LookPath as lookFn in production; tests inject a fake.
func checkHostBinary(name string, lookFn lookPathFn) CheckResult {
	path, err := lookFn(name)
	if err != nil {
		return CheckResult{
			Kind:   "host-dep",
			Check:  name,
			Status: StatusFail,
			Detail: "not on PATH",
			Remedy: installHint(name),
		}
	}
	return CheckResult{
		Kind:   "host-dep",
		Check:  name,
		Status: StatusPass,
		Detail: path,
	}
}

// checkDockerDaemon runs `docker info` (via infoFn) and reports whether
// the daemon answers. Caps the probe at 3 seconds to keep the doctor
// report responsive. Emits StatusWarn (not Fail) when the binary is on
// PATH but the daemon is unreachable — the operator can keep going with
// `make install` and `sextant init`, but `sextant start` will need the
// daemon up.
func checkDockerDaemon(ctx context.Context, lookFn lookPathFn, infoFn dockerInfoFn) CheckResult {
	if _, err := lookFn("docker"); err != nil {
		return CheckResult{
			Kind:   "host-dep",
			Check:  "docker-daemon",
			Status: StatusWarn,
			Detail: "docker binary missing; skipping daemon probe",
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := infoFn(ctx); err != nil {
		return CheckResult{
			Kind:   "host-dep",
			Check:  "docker-daemon",
			Status: StatusWarn,
			Detail: fmt.Sprintf("daemon not reachable: %v", err),
			Remedy: "start OrbStack / Docker Desktop",
		}
	}
	return CheckResult{
		Kind:   "host-dep",
		Check:  "docker-daemon",
		Status: StatusPass,
		Detail: "reachable",
	}
}

// defaultDockerInfo is the production implementation: shells out to
// `docker info`. Tests pass a fake.
func defaultDockerInfo(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// minGoVersion is the floor declared by go.mod. Keep in sync with the
// `go` directive there.
const minGoVersion = "1.26"

// checkGoVersion verifies `go version` reports at least minGoVersion.
// Returns a Fail with the right remedy when the binary is missing or
// the version is too old.
func checkGoVersion(lookFn lookPathFn, runFn func(string, ...string) ([]byte, error)) CheckResult {
	if _, err := lookFn("go"); err != nil {
		return CheckResult{
			Kind: "host-dep", Check: "go", Status: StatusFail,
			Detail: "not on PATH", Remedy: installHint("go"),
		}
	}
	out, err := runFn("go", "version")
	if err != nil {
		return CheckResult{
			Kind: "host-dep", Check: "go", Status: StatusFail,
			Detail: fmt.Sprintf("`go version` failed: %v", err),
		}
	}
	// `go version` output: "go version go1.26.1 darwin/arm64"
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return CheckResult{
			Kind: "host-dep", Check: "go", Status: StatusFail,
			Detail: "unparseable `go version` output: " + string(out),
		}
	}
	got := strings.TrimPrefix(fields[2], "go")
	if compareSemver(got, minGoVersion) < 0 {
		return CheckResult{
			Kind: "host-dep", Check: "go", Status: StatusFail,
			Detail: fmt.Sprintf("go %s found, need >= %s", got, minGoVersion),
			Remedy: installHint("go"),
		}
	}
	return CheckResult{
		Kind: "host-dep", Check: "go", Status: StatusPass,
		Detail: fmt.Sprintf("go %s", got),
	}
}

// compareSemver compares two dotted-number versions (e.g. "1.26.1" vs
// "1.26"). Returns -1, 0, or +1. Non-numeric segments compare lexically.
// Sufficient for Go-style versions; intentionally not a full semver
// implementation.
func compareSemver(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			_, _ = fmt.Sscanf(as[i], "%d", &av)
		}
		if i < len(bs) {
			_, _ = fmt.Sscanf(bs[i], "%d", &bv)
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return +1
		}
	}
	return 0
}

// defaultRunCmd is the production implementation of the runFn used by
// checkGoVersion. Tests pass a fake.
func defaultRunCmd(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// collectHostDepChecks returns one row per host dependency, in display
// order. When contributor is false, contributor-only deps are skipped.
// lookFn / infoFn / runFn are indirected so tests don't depend on the
// host PATH or a running docker daemon.
func collectHostDepChecks(ctx context.Context, contributor bool, lookFn lookPathFn, infoFn dockerInfoFn, runFn func(string, ...string) ([]byte, error)) []CheckResult {
	var out []CheckResult
	for _, d := range hostDeps {
		if d.Contributor && !contributor {
			continue
		}
		if d.Name == "go" {
			out = append(out, checkGoVersion(lookFn, runFn))
			continue
		}
		out = append(out, checkHostBinary(d.Name, lookFn))
	}
	// Docker daemon row always emitted.
	out = append(out, checkDockerDaemon(ctx, lookFn, infoFn))
	return out
}

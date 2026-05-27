# Onboarding + `make bootstrap` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a layered operator/contributor README, a CLAUDE.md onboarding pointer, host-binary preflight checks in `sextant doctor`, and a `make bootstrap` target that takes a fresh macOS host to a working install in one command.

**Architecture:** Bash script does the toolchain bootstrap (since no sextant binary exists yet); once `make install` produces the binary, the script delegates to `sextant doctor --preflight` for the richer Go-side verification. Doctor preflight checks are unit-testable via injected lookup functions. README and mdbook both point at `make bootstrap` as the lead, with the manual install path preserved.

**Tech Stack:** Go (`cmd/sextant/doctor.go`), Bash (`scripts/bootstrap.sh`), Make (`Makefile`), Markdown (`README.md`, `CLAUDE.md`, `docs/book/src/getting-started/install.md`).

**Spec:** [`docs/superpowers/specs/2026-05-26-onboarding-and-bootstrap-design.md`](../specs/2026-05-26-onboarding-and-bootstrap-design.md)

---

## File map

**Create:**
- `scripts/bootstrap.sh` — green-field setup script (~150 lines, bash)
- `cmd/sextant/preflight.go` — host-dep check functions, isolated for unit testing
- `cmd/sextant/preflight_test.go` — unit tests for the host-dep checks (uses injected `lookPathFn` for hermeticity)

**Modify:**
- `README.md` — full rewrite per spec §1
- `CLAUDE.md` — add "Helping someone onboard" section per spec §2
- `cmd/sextant/doctor.go` — add `--preflight` and `--contributor` flags; wire `collectHostDepChecks` into `collectChecks` and into a new preflight-only path
- `Makefile` — add `bootstrap` target
- `docs/book/src/getting-started/install.md` — restructure into automated/manual paths per spec §5

**Implementation order rationale:** Doctor preflight first (foundational — `bootstrap.sh` calls it); then `bootstrap.sh`; then the Makefile target; then README and mdbook (so they reference a `make bootstrap` that actually exists); CLAUDE.md last (smallest, can ship independently).

---

## Task 1: Doctor preflight — host-dep check functions

**Files:**
- Create: `cmd/sextant/preflight.go`
- Create: `cmd/sextant/preflight_test.go`

**What this builds:** Pure functions that probe for `nats-server`, `clickhouse`, `docker` binary, `docker` daemon, `go`, `node`, `npm` on the host. Each returns a `CheckResult` (the existing type in `doctor.go:36-47`). Injectable lookup function for tests.

### Steps

- [ ] **Step 1.1: Create `cmd/sextant/preflight.go` with the dependency types**

```go
// cmd/sextant/preflight.go
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
```

- [ ] **Step 1.2: Add the per-binary install hints**

Append to `cmd/sextant/preflight.go`:

```go
// installHint returns the platform-specific remedy string for a missing
// binary. Falls back to a generic "install <name>" when the platform is
// unrecognized (the operator gets *something* actionable, even if it's
// not a copy-paste line).
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
			return "brew install go (>= 1.26)"
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
```

- [ ] **Step 1.3: Implement `checkHostBinary`**

Append to `cmd/sextant/preflight.go`:

```go
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
```

- [ ] **Step 1.4: Implement `checkDockerDaemon`**

Append to `cmd/sextant/preflight.go`:

```go
// checkDockerDaemon runs `docker info` (via infoFn) and reports whether
// the daemon answers. Emits StatusWarn (not Fail) when the binary is on
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
			Status: StatusFail,
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
```

- [ ] **Step 1.5: Implement the Go version check**

Append to `cmd/sextant/preflight.go`:

```go
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
			fmt.Sscanf(as[i], "%d", &av)
		}
		if i < len(bs) {
			fmt.Sscanf(bs[i], "%d", &bv)
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
```

- [ ] **Step 1.6: Implement `collectHostDepChecks` — the aggregator**

Append to `cmd/sextant/preflight.go`:

```go
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
	// Docker daemon row always emitted (the only "is-it-running" probe
	// in the preflight set).
	out = append(out, checkDockerDaemon(ctx, lookFn, infoFn))
	return out
}
```

- [ ] **Step 1.7: Write the failing unit tests**

Create `cmd/sextant/preflight_test.go`:

```go
package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeLookup returns a lookPathFn that resolves only the names in
// present. Names not in present return exec.ErrNotFound.
func fakeLookup(present ...string) lookPathFn {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/fake/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func okDocker(context.Context) error  { return nil }
func badDocker(context.Context) error { return errors.New("connection refused") }

// fakeGoVersion returns a runFn that simulates `go version` output.
func fakeGoVersion(ver string) func(string, ...string) ([]byte, error) {
	return func(_ string, _ ...string) ([]byte, error) {
		return []byte("go version go" + ver + " darwin/arm64\n"), nil
	}
}

func TestPreflight_AllPresent(t *testing.T) {
	lookFn := fakeLookup("nats-server", "clickhouse", "docker", "go", "node", "npm")
	results := collectHostDepChecks(context.Background(), true, lookFn, okDocker, fakeGoVersion("1.26.1"))
	for _, r := range results {
		if r.Status != StatusPass {
			t.Errorf("check %s: status = %s, want pass (%s)", r.Check, r.Status, r.Detail)
		}
	}
}

func TestPreflight_NatsMissing(t *testing.T) {
	lookFn := fakeLookup("clickhouse", "docker")
	results := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "nats-server" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no nats-server row in results")
	}
	if row.Status != StatusFail {
		t.Errorf("nats-server status = %s, want fail", row.Status)
	}
	if !strings.Contains(row.Remedy, "nats-server") {
		t.Errorf("remedy = %q, want it to mention nats-server", row.Remedy)
	}
}

func TestPreflight_DockerBinaryPresentDaemonDown(t *testing.T) {
	lookFn := fakeLookup("docker")
	results := collectHostDepChecks(context.Background(), false, lookFn, badDocker, fakeGoVersion("1.26.1"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "docker-daemon" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no docker-daemon row in results")
	}
	if row.Status != StatusFail {
		t.Errorf("docker-daemon status = %s, want fail", row.Status)
	}
	if !strings.Contains(strings.ToLower(row.Remedy), "orbstack") && !strings.Contains(strings.ToLower(row.Remedy), "docker") {
		t.Errorf("remedy = %q, want it to mention OrbStack or Docker", row.Remedy)
	}
}

func TestPreflight_DockerBinaryMissingDaemonProbeWarns(t *testing.T) {
	lookFn := fakeLookup() // nothing present
	results := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "docker-daemon" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no docker-daemon row in results")
	}
	if row.Status != StatusWarn {
		t.Errorf("docker-daemon status = %s, want warn (binary missing so daemon probe skipped)", row.Status)
	}
}

func TestPreflight_ContributorModeIncludesGoNodeNpm(t *testing.T) {
	lookFn := fakeLookup("nats-server", "clickhouse", "docker", "go", "node", "npm")
	contrib := collectHostDepChecks(context.Background(), true, lookFn, okDocker, fakeGoVersion("1.26.1"))
	operator := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))
	seen := func(rs []CheckResult, name string) bool {
		for _, r := range rs {
			if r.Check == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"go", "node", "npm"} {
		if !seen(contrib, name) {
			t.Errorf("contributor mode missing %s row", name)
		}
		if seen(operator, name) {
			t.Errorf("operator mode unexpectedly includes %s row", name)
		}
	}
}

func TestPreflight_GoTooOld(t *testing.T) {
	lookFn := fakeLookup("go")
	results := collectHostDepChecks(context.Background(), true, lookFn, okDocker, fakeGoVersion("1.20.0"))
	var row *CheckResult
	for i := range results {
		if results[i].Check == "go" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatal("no go row in results")
	}
	if row.Status != StatusFail {
		t.Errorf("go status = %s, want fail (too old)", row.Status)
	}
	if !strings.Contains(row.Detail, "1.20") {
		t.Errorf("detail = %q, want it to mention the found version", row.Detail)
	}
}
```

- [ ] **Step 1.8: Run the tests — they should fail because nothing's wired up yet**

Run: `cd /Users/lena/dev/sextant && go test -run TestPreflight ./cmd/sextant/`
Expected: FAIL or compile error referring to undefined symbols.

If it fails with "compile error: hostDeps undefined" or similar — that's the moment the implementation needs to compile clean (Step 1.1–1.6 should make this work). Re-run; expect PASS.

- [ ] **Step 1.9: Run the full package tests to confirm no regression**

Run: `cd /Users/lena/dev/sextant && go test -race -count=1 ./cmd/sextant/`
Expected: PASS — including existing `TestDoctorAgainstFreshInit` etc.

- [ ] **Step 1.10: Commit**

```bash
git add cmd/sextant/preflight.go cmd/sextant/preflight_test.go
git commit -m "$(cat <<'EOF'
feat(doctor): host-dep preflight check functions

Adds collectHostDepChecks and per-binary probes for nats-server,
clickhouse, docker (binary + daemon), go, node, npm. Lookup,
docker-info, and run functions are indirected so the unit tests
don't depend on the host PATH or a running docker daemon.

Not yet wired into the CLI; that lands in the next commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Wire `--preflight` and `--contributor` flags into `sextant doctor`

**Files:**
- Modify: `cmd/sextant/doctor.go`

**What this builds:** Two new flags. `--preflight` runs only the host-dep checks (no config required). `--contributor` adds Go/Node/npm to the dep list. Both compose.

### Steps

- [ ] **Step 2.1: Add the flags to `runDoctor`**

In `cmd/sextant/doctor.go`, locate `runDoctor` (around line 63) and add two new flags after the existing `asJSON` flag:

```go
preflight := fs.Bool("preflight", false, "host-dep checks only (skips config, daemon, NATS, ClickHouse)")
contributor := fs.Bool("contributor", false, "additionally check contributor deps (go, node, npm)")
```

Then thread them into the collection call. Replace the existing block:

```go
results := collectChecks(ctx, cfgDir, dataDirAbs)
```

with:

```go
var results []CheckResult
if *preflight {
	results = collectHostDepChecks(ctx, *contributor, exec.LookPath, defaultDockerInfo, defaultRunCmd)
} else {
	results = collectChecks(ctx, cfgDir, dataDirAbs, *contributor)
}
```

- [ ] **Step 2.2: Update `collectChecks` to splice host-dep rows in front of the existing checks**

In `cmd/sextant/doctor.go`, update the `collectChecks` signature:

```go
func collectChecks(ctx context.Context, cfgDir, dataDir string, contributor bool) []CheckResult {
```

At the very top of the function body (immediately after the `var out []CheckResult` line), prepend the host-dep rows:

```go
out = append(out, collectHostDepChecks(ctx, contributor, exec.LookPath, defaultDockerInfo, defaultRunCmd)...)
```

**Update test callers.** Changing `collectChecks`'s signature breaks `cmd/sextant/doctor_test.go`, which calls `collectChecks(ctx, opts.ConfigDir, opts.DataDir)` in `TestDoctorAgainstFreshInit` (around line 22) and `TestDoctorReportsCorruptedCA` (around line 72). Append `, false` to every call site to pass the new `contributor` argument:

```go
// Old:
results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)
// New:
results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)
```

Search for any other callers with: `grep -n "collectChecks(" cmd/sextant/*.go`.

- [ ] **Step 2.3: Update the doctor usage string**

In `cmd/sextant/doctor.go`, replace `doctorUsage` (around line 90) with:

```go
const doctorUsage = `usage: sextant doctor [--config-dir PATH] [--data-dir PATH] [--json] [--preflight] [--contributor]

Runs health diagnostics against the installation rooted at the given
config and data dirs (defaults: ~/.config/sextant, ~/.local/share/sextant).

--preflight runs only host-dep checks (nats-server, clickhouse, docker)
and skips anything that needs config to exist. Use it before
sextant init has ever been run, or from scripts/bootstrap.sh.

--contributor additionally checks deps needed to build sextant from
source (go, node, npm). Off by default; operators using installed
binaries don't need it.

Exit code 0 on all-pass (or only "not running" warnings), 2 on failure.`
```

- [ ] **Step 2.4: Run the full test suite**

Run: `cd /Users/lena/dev/sextant && go test -race -count=1 ./cmd/sextant/`
Expected: PASS — including the existing `TestDoctorAgainstFreshInit` which now sees an extra column of host-dep rows.

If `TestDoctorAgainstFreshInit` fails because it doesn't expect host-dep rows: it asserts on specific kinds via `wantPass` and counts `failures` — read the test, decide whether host-dep rows should also be `Pass` on the test host (likely yes, since the test machine has nats-server etc.) or whether the test should explicitly ignore `host-dep` kinds. Prefer the latter: add `host-dep` to the kinds the test tolerates (it's environmental, not under the test's control).

If a fix is needed in `doctor_test.go`:

```go
// In TestDoctorAgainstFreshInit, near the failures loop:
for _, r := range results {
	if r.Kind == "host-dep" {
		// Environmental; not what this test exercises.
		continue
	}
	// ... existing assertions
}
```

- [ ] **Step 2.5: Manually verify the CLI surface**

```bash
go build -o /tmp/sextant ./cmd/sextant
/tmp/sextant doctor --help
/tmp/sextant doctor --preflight
/tmp/sextant doctor --preflight --contributor
```

Expected: `--help` shows the new flags. `--preflight` runs without needing config (exit 0 or 2 depending on whether deps are present on this host). `--preflight --contributor` additionally shows go/node/npm rows.

- [ ] **Step 2.6: Commit**

```bash
git add cmd/sextant/doctor.go cmd/sextant/doctor_test.go
git commit -m "$(cat <<'EOF'
feat(doctor): --preflight and --contributor flags

--preflight runs only the new host-dep checks (no config required),
so scripts/bootstrap.sh can call it on a fresh machine. --contributor
adds go/node/npm to the dep list for people building from source.

Both flags compose. Default doctor output now includes host-dep rows
as the first section of the report.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `scripts/bootstrap.sh`

**Files:**
- Create: `scripts/bootstrap.sh`

**What this builds:** The bash script that turns a fresh macOS host into a working sextant install in one command. Interactive Y/n with `--yes` bypass. Idempotent. macOS via brew, partial Linux via apt.

### Steps

- [ ] **Step 3.1: Create `scripts/` directory if missing**

```bash
mkdir -p /Users/lena/dev/sextant/scripts
```

- [ ] **Step 3.2: Write `scripts/bootstrap.sh`**

Create the file with this content:

```bash
#!/usr/bin/env bash
# scripts/bootstrap.sh — green-field setup for sextant.
#
# Idempotent: safe to re-run after git pull, or to recover a
# half-broken install.
#
# Usage:
#   ./scripts/bootstrap.sh           # interactive
#   ./scripts/bootstrap.sh --yes     # non-interactive (CI / repeat runs)
#   ./scripts/bootstrap.sh --skip-init   # skip the `sextant init` step
#   ./scripts/bootstrap.sh --help

set -euo pipefail

YES=0
SKIP_INIT=0

usage() {
  cat <<EOF
usage: scripts/bootstrap.sh [--yes] [--skip-init] [--help]

Brings a fresh host to a working sextant install:
  1. Audit host deps (Go, nats-server, clickhouse, docker, node)
  2. Print the install plan and prompt Y/n
  3. brew (macOS) or apt (Linux) install missing deps
  4. make install
  5. sextant doctor --preflight
  6. sextant init

--yes / -y     non-interactive; assume yes to the install prompt
--skip-init    install deps and binaries but don't write config
--help / -h    print this message

macOS via brew is the tested path. Linux via apt is partial —
nats-server and clickhouse aren't in default apt repos, so the
script prints upstream URLs and exits if they're missing.
EOF
}

for arg in "$@"; do
  case "$arg" in
    -y|--yes) YES=1 ;;
    --skip-init) SKIP_INIT=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; usage; exit 2 ;;
  esac
done

confirm() {
  if [[ "$YES" == "1" ]]; then
    return 0
  fi
  local prompt="$1"
  read -r -p "$prompt [Y/n] " ans
  case "${ans:-y}" in
    [Yy]*) return 0 ;;
    *) echo "aborted."; exit 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# 1. Hard prerequisites
# ---------------------------------------------------------------------------
for tool in make git uname; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required tool '$tool' not found." >&2
    echo "install Xcode Command Line Tools (macOS: xcode-select --install) or build-essential (Linux)." >&2
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# 2. Detect OS + package manager
# ---------------------------------------------------------------------------
OS="$(uname -s)"
PKGMGR=""
case "$OS" in
  Darwin)
    if ! command -v brew >/dev/null 2>&1; then
      echo "Homebrew not found. Install it from https://brew.sh and re-run." >&2
      exit 1
    fi
    PKGMGR=brew
    ;;
  Linux)
    if command -v apt-get >/dev/null 2>&1; then
      PKGMGR=apt
    elif command -v brew >/dev/null 2>&1; then
      PKGMGR=brew
    else
      echo "no supported package manager (apt or brew) found." >&2
      exit 1
    fi
    ;;
  *)
    echo "unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# ---------------------------------------------------------------------------
# 3. Audit phase — no side effects
# ---------------------------------------------------------------------------
need_go=0
go_detail=""
if ! command -v go >/dev/null 2>&1; then
  need_go=1
  go_detail="missing"
else
  go_ver=$(go version | awk '{print $3}' | sed 's/go//')
  # Compare to 1.26 using sort -V
  if ! printf '1.26\n%s\n' "$go_ver" | sort -V -C 2>/dev/null; then
    need_go=1
    go_detail="found $go_ver, need >= 1.26"
  fi
fi

declare -a need_deps=()
for dep in nats-server clickhouse docker node; do
  if ! command -v "$dep" >/dev/null 2>&1; then
    need_deps+=("$dep")
  fi
done

docker_daemon_note=""
if command -v docker >/dev/null 2>&1; then
  if ! docker info >/dev/null 2>&1; then
    docker_daemon_note="docker binary present but daemon not reachable; start OrbStack manually"
  fi
fi

# ---------------------------------------------------------------------------
# 4. Linux escape hatch — bail if asked to install deps apt doesn't ship
# ---------------------------------------------------------------------------
if [[ "$PKGMGR" == "apt" ]]; then
  blockers=()
  for d in "${need_deps[@]}"; do
    if [[ "$d" == "nats-server" || "$d" == "clickhouse" ]]; then
      blockers+=("$d")
    fi
  done
  if [[ ${#blockers[@]} -gt 0 ]]; then
    echo "Linux apt path can't install: ${blockers[*]}"
    echo "Install them manually:"
    for b in "${blockers[@]}"; do
      case "$b" in
        nats-server) echo "  - nats-server: https://github.com/nats-io/nats-server/releases" ;;
        clickhouse)  echo "  - clickhouse:  https://clickhouse.com/docs/en/install" ;;
      esac
    done
    echo "Re-run scripts/bootstrap.sh after they're on PATH."
    exit 1
  fi
fi

# ---------------------------------------------------------------------------
# 5. Plan + confirm
# ---------------------------------------------------------------------------
echo "=== sextant bootstrap plan ==="
echo "Package manager: $PKGMGR"
if [[ "$OS" == "Linux" ]]; then
  echo "Note: Linux path is unverified; macOS is the tested target."
fi
echo ""

planned=0
if [[ "$need_go" == "1" ]]; then
  echo "  - install Go (>= 1.26) [$go_detail]"
  planned=1
fi
for d in "${need_deps[@]}"; do
  case "$d" in
    docker) echo "  - install OrbStack (docker; install Docker Desktop manually if you prefer)" ;;
    *)      echo "  - install $d" ;;
  esac
  planned=1
done
if [[ -n "$docker_daemon_note" ]]; then
  echo "  note: $docker_daemon_note"
fi
echo "  - make install"
echo "  - sextant doctor --preflight"
if [[ "$SKIP_INIT" == "0" ]]; then
  echo "  - sextant init"
fi
echo ""

if [[ "$planned" == "1" ]]; then
  confirm "Proceed?"
else
  echo "All dependencies present."
fi

# ---------------------------------------------------------------------------
# 6. Install Go first (required for make install)
# ---------------------------------------------------------------------------
if [[ "$need_go" == "1" ]]; then
  case "$PKGMGR" in
    brew) brew install go ;;
    apt)
      echo "apt path: see https://go.dev/dl for >= 1.26; apt's golang is usually too old."
      exit 1
      ;;
  esac
fi

# ---------------------------------------------------------------------------
# 7. Install runtime deps
# ---------------------------------------------------------------------------
for d in "${need_deps[@]}"; do
  case "$PKGMGR" in
    brew)
      case "$d" in
        docker) brew install --cask orbstack ;;
        *)      brew install "$d" ;;
      esac
      ;;
    apt)
      case "$d" in
        docker) sudo apt-get install -y docker.io ;;
        node)   sudo apt-get install -y nodejs npm ;;
      esac
      ;;
  esac
done

# ---------------------------------------------------------------------------
# 8. make install
# ---------------------------------------------------------------------------
make install

# ---------------------------------------------------------------------------
# 9. Preflight: now sextant exists, run the Go-side check
# ---------------------------------------------------------------------------
SEXTANT="${HOME}/.local/bin/sextant"
if [[ ! -x "$SEXTANT" ]]; then
  echo "make install completed but $SEXTANT is not executable." >&2
  exit 1
fi
if ! "$SEXTANT" doctor --preflight; then
  echo ""
  echo "preflight failed; resolve the issues above and re-run."
  exit 1
fi

# ---------------------------------------------------------------------------
# 10. sextant init
# ---------------------------------------------------------------------------
if [[ "$SKIP_INIT" == "0" ]]; then
  "$SEXTANT" init
fi

echo ""
echo "Bootstrap complete."
echo "Next: sextant start && sextant doctor"
```

- [ ] **Step 3.3: Make it executable**

```bash
chmod +x /Users/lena/dev/sextant/scripts/bootstrap.sh
```

- [ ] **Step 3.4: Run shellcheck**

```bash
shellcheck /Users/lena/dev/sextant/scripts/bootstrap.sh
```

Expected: clean exit (no findings). If shellcheck isn't installed, `brew install shellcheck` first. If the script has findings, fix them inline — common ones are quoting issues around `${var}` expansions and `[[ ]]` vs `[ ]` use.

- [ ] **Step 3.5: Smoke-test locally without running brew installs**

This host already has every dep installed. Running `bash scripts/bootstrap.sh --yes` should:
1. Print "All dependencies present."
2. Run `make install` (rebuilds binaries — a few seconds).
3. Run `sextant doctor --preflight` — green.
4. Run `sextant init` — idempotent, all "existing" lines.
5. Print "Bootstrap complete. Next: ..."

Run: `bash /Users/lena/dev/sextant/scripts/bootstrap.sh --yes`
Expected: exit 0; final line is "Bootstrap complete. Next: sextant start && sextant doctor".

- [ ] **Step 3.6: Smoke-test the help output**

Run: `bash /Users/lena/dev/sextant/scripts/bootstrap.sh --help`
Expected: prints usage, exit 0.

Run: `bash /Users/lena/dev/sextant/scripts/bootstrap.sh --bogus-flag`
Expected: prints "unknown flag: --bogus-flag", exit 2.

- [ ] **Step 3.7: Commit**

```bash
git add scripts/bootstrap.sh
git commit -m "$(cat <<'EOF'
feat(scripts): bootstrap.sh for green-field install

One command from clean macOS to a working sextant: audits host deps,
brew-installs whatever's missing (with Y/n prompt; --yes for CI),
runs make install, calls sextant doctor --preflight, then sextant init.
Idempotent — safe to re-run after git pull or to recover a half-broken
install.

Linux apt path is partial (nats-server and clickhouse aren't in
default apt); the script bails with upstream URLs if those are missing.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `make bootstrap` target

**Files:**
- Modify: `Makefile`

**What this builds:** The `make bootstrap` entry point. Thin Makefile target that calls `scripts/bootstrap.sh`.

### Steps

- [ ] **Step 4.1: Add `bootstrap` to `.PHONY`**

In `/Users/lena/dev/sextant/Makefile`, locate the `.PHONY` line (around line 25) and append `bootstrap`:

```make
.PHONY: all fmt lint lint-go lint-nilaway lint-ts lint-sidecar test test-go test-ts test-sidecar build clean tidy install install-tools uninstall bootstrap \
        ts-install ts-codegen ts-lint ts-test ts-build \
        sidecar-install sidecar-image sidecar-image-test
```

- [ ] **Step 4.2: Add the `bootstrap` target**

Add this target to `Makefile`, immediately after the existing `install-tools` target (around line 133):

```make
## bootstrap — green-field setup: host deps + build + install + init.
##             Interactive; prompts before brew-installing. Pass YES=1 for
##             non-interactive (CI / repeat runs). Pass SKIP_INIT=1 to
##             skip `sextant init`.
bootstrap:
	@bash scripts/bootstrap.sh \
	  $(if $(YES),--yes,) \
	  $(if $(SKIP_INIT),--skip-init,)
```

- [ ] **Step 4.3: Smoke-test the make target**

Run: `cd /Users/lena/dev/sextant && make bootstrap YES=1`
Expected: identical to `bash scripts/bootstrap.sh --yes` from Task 3.5 — exit 0, "Bootstrap complete."

- [ ] **Step 4.4: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
feat(make): bootstrap target wrapping scripts/bootstrap.sh

Single entry point: `make bootstrap` (interactive) or `make bootstrap
YES=1` (non-interactive). SKIP_INIT=1 passes --skip-init through to
the script. Matches PRINCIPLES.md "single command, end to end".

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: README rewrite

**Files:**
- Modify: `README.md`

**What this builds:** A layered onboarding README: operator path first, contributor path second. Replaces the stale "no code yet" framing.

### Steps

- [ ] **Step 5.1: Replace `README.md` end-to-end**

Overwrite `/Users/lena/dev/sextant/README.md` with:

```markdown
# sextant

A Go control plane for AI coding agents. Sextant supervises a NATS JetStream bus, a ClickHouse store, and one Docker container per running agent — each container drives the Claude Agent SDK and reports back over the bus.

## Quickstart (operators)

```bash
git clone git@github.com:love-lena/sextant.git
cd sextant
make bootstrap            # installs host deps, builds, installs, runs `sextant init`
sextant start             # bring up the daemon
sextant agents spawn assistant --template default
sextant conversation assistant
```

`make bootstrap` audits host deps (Go ≥ 1.26, `nats-server`, `clickhouse`, `docker`/OrbStack, `node`), prints what it's about to brew-install, prompts `Y/n`, then chains `make install` → `sextant doctor --preflight` → `sextant init`. Pass `YES=1` for non-interactive (CI / repeat runs).

macOS via Homebrew is the tested path. Linux is partial — `nats-server` and `clickhouse` aren't in default apt repos, so on Linux the script bails with upstream URLs if those are missing.

For a deeper walkthrough — what each step writes, how the daemon is supervised, where logs land — read [`docs/book/src/getting-started/first-run.md`](docs/book/src/getting-started/first-run.md).

### Verifying

```bash
sextant doctor
```

Runs ~15 checks: config files present, CA keypair valid, sextantd reachable, NATS and ClickHouse running, host binaries on PATH, installed binary's `GitSHA` matches the repo. Exit code `0` on green, `2` if anything failed. `sextant doctor --preflight` runs only the host-binary checks (faster, doesn't need the daemon running).

> **macOS gotcha — do not use plain `cp`.** `cp bin/sextant ~/.local/bin/` stamps `com.apple.provenance` onto the destination, and Gatekeeper SIGKILLs the resulting binary on invocation (exit 137, **no stderr**). The failure looks like the binary itself is broken. `make install` (which `make bootstrap` uses) invokes `/usr/bin/install`, which writes a clean file. Cross-reference: [`plans/issues/docs-install-via-make-install-not-cp.md`](plans/issues/docs-install-via-make-install-not-cp.md).

### Where to go next

The reference book lives in [`docs/book/`](docs/book/). Run `mdbook serve docs/book` to browse in a browser, or open the `.md` files directly.

- [CLI reference](docs/book/src/operator-guide/cli.md) — every `sextant <subcommand>`
- [TUIs](docs/book/src/operator-guide/tuis.md) — `sextant conversation`, `sextant-tui-agents`
- [Templates](docs/book/src/operator-guide/templates.md) — defining new agent kinds
- [Worktrees](docs/book/src/operator-guide/worktrees.md) — how agents work in isolated git worktrees
- [Architecture overview](docs/book/src/architecture/overview.md) — the why behind the design

## Contributing

If you're working *on* sextant rather than driving it:

- **[`PRINCIPLES.md`](PRINCIPLES.md)** — three load-bearing values that constrain every feature decision. Read once.
- **[`CLAUDE.md`](CLAUDE.md)** — auto-loaded project guidance for AI agents (and a useful orientation for humans too).
- **[`conventions/`](conventions/)** — Go style, git workflow, TUI patterns, operator-experience conventions.
- **[`plans/bootstrap.md`](plans/bootstrap.md)** — the M0–M17 milestone plan. M0–M15 are merged on `main`; M16 (self-update) and M17 (test environments) are not implemented.
- **[`plans/issues/`](plans/issues/)** — open + closed bugs and feature requests, one markdown file per issue.

For the build/test/lint loop, after `make bootstrap`:

```bash
make test       # go test -race + TS vitest + sidecar tests
make lint       # golangci-lint + nilaway + tsc --noEmit
make install    # rebuild and reinstall binaries to ~/.local/bin
```

Worktree-based feature work uses the `EnterWorktree` tool (Claude Code) or `git worktree add` directly — see [`conventions/git-workflow.md`](conventions/git-workflow.md).

Every commit carries `Co-Authored-By: <model> <noreply@anthropic.com>` when an AI participated. If you spawn subagents to commit, tell them to include the trailer too.

---

The earlier experimental Rust version, code-named "pilot" (v0), lives archived at [`love-lena/sextant-pilot`](https://github.com/love-lena/sextant-pilot). No code carryover; design reconsidered top-to-bottom for v1.
```

- [ ] **Step 5.2: Verify the links resolve**

```bash
cd /Users/lena/dev/sextant
for path in PRINCIPLES.md CLAUDE.md conventions plans/bootstrap.md plans/issues \
            docs/book/src/getting-started/first-run.md \
            docs/book/src/operator-guide/cli.md \
            docs/book/src/operator-guide/tuis.md \
            docs/book/src/operator-guide/templates.md \
            docs/book/src/operator-guide/worktrees.md \
            docs/book/src/architecture/overview.md \
            plans/issues/docs-install-via-make-install-not-cp.md; do
  [[ -e "$path" ]] && echo "OK  $path" || echo "FAIL $path"
done
```

Expected: every line prints `OK`. If any prints `FAIL`, fix the link in the README (the file may not exist at that path — check `ls docs/book/src/operator-guide/` for the actual filenames).

- [ ] **Step 5.3: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(README): rewrite as layered operator/contributor onboarding

Replaces the stale "specifications, plans, and conventions only.
No code yet" framing. Operator quickstart leads with `make bootstrap`;
contributor section points at PRINCIPLES, conventions, and the
milestone plan. macOS Gatekeeper warning preserved.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Restructure `docs/book/src/getting-started/install.md`

**Files:**
- Modify: `docs/book/src/getting-started/install.md`

**What this builds:** Two clearly-labeled paths in the install chapter — automated (`make bootstrap`) on top, manual (the existing dep table) below.

### Steps

- [ ] **Step 6.1: Replace `docs/book/src/getting-started/install.md` end-to-end**

Overwrite the file with:

```markdown
# Install

There are two paths: the **automated path** (one command, prompts before installing anything) and the **manual path** (install every dep yourself, then build). The automated path is what most operators want.

> macOS via Homebrew is the tested target. Linux is partial — `nats-server` and `clickhouse` aren't in default apt repos, and the bootstrap script will bail with upstream URLs if they're missing.

## Automated

From a fresh checkout:

```bash
make bootstrap
```

This calls [`scripts/bootstrap.sh`](https://github.com/love-lena/sextant/blob/main/scripts/bootstrap.sh), which:

1. Audits host deps (Go ≥ 1.26, `nats-server`, `clickhouse`, `docker`/OrbStack, `node`)
2. Prints the install plan and prompts `Y/n`
3. `brew install`s whatever's missing (macOS) — OrbStack as the docker default
4. Runs `make install` (builds and installs every `cmd/` binary to `~/.local/bin`)
5. Runs `sextant doctor --preflight` to confirm
6. Runs `sextant init` to generate config, CA, and the default template

Pass `YES=1` for non-interactive use (CI, repeat runs). Pass `SKIP_INIT=1` to skip step 6 if you'd rather manage `~/.config/sextant/` yourself.

Re-running after `git pull` is safe: brew steps are no-ops, `make install` rebuilds, `sextant init` is idempotent.

## Manual

If you'd rather install everything yourself (or you're on Linux):

### Host dependencies

| Dependency      | Why                                                                                 | Install                                                                |
|-----------------|-------------------------------------------------------------------------------------|------------------------------------------------------------------------|
| **Go ≥ 1.26**   | Module declares `go 1.26` (`go.mod:3`). Older toolchains will refuse to build.      | macOS: `brew install go`. Linux: see <https://go.dev/dl>.              |
| **NATS server** | `sextantd` execs it as a subprocess.                                                | macOS: `brew install nats-server`. Linux: <https://github.com/nats-io/nats-server/releases>. |
| **ClickHouse server** | `sextantd` execs it as a subprocess.                                          | macOS: `brew install clickhouse`. Linux: <https://clickhouse.com/docs/en/install>. |
| **Docker** (OrbStack on macOS) | Each agent runs in a container.                                          | `brew install --cask orbstack` or Docker Desktop.                      |
| **Node + npm**  | Building the TypeScript client + the sidecar image.                                 | macOS: `brew install node`. Linux: `apt install nodejs npm`.            |
| **`golangci-lint`, `nilaway`** | CI gates — only needed if you intend to run `make lint`.              | `make install-tools` (`Makefile:134`) installs both.                   |

The host must have a container runtime — there is no bare-process fallback for the sidecar (`specs/architecture.md` §3).

### Build and install

From a checkout:

```bash
make install
```

`make install` builds every binary under `cmd/` and writes them to `$PREFIX/bin` (default `$HOME/.local/bin`; `Makefile:19-20,116-120`). The `CMDS` variable at `Makefile:23` is the authoritative list: `sextant`, `sextantd`, `sextant-shipper`, `sextant-natsboot`, `sextant-clickhouseboot`, `sextant-client-demo`, `sextant-tui-agents`.

Override the destination for a system-wide install:

```bash
sudo make install PREFIX=/usr/local
```

The Makefile uses `/usr/bin/install` rather than `cp`. On macOS, plain `cp` stamps `com.apple.provenance` onto the destination, and Gatekeeper then SIGKILLs the resulting binary on launch (exit 137, no stderr). `make install` sidesteps that. Cross-reference: `plans/issues/docs-install-via-make-install-not-cp.md`. Linux is unaffected.

`make uninstall` removes the installed binaries (`Makefile:122-127`).

### Generate config

```bash
sextant init
```

See [First run](./first-run.md) for what this writes.

## Build the sidecar image

The agent container image is built separately because it's an opt-in multi-MB pull (`Makefile:142-157`):

```bash
make sidecar-image
```

This produces `sextant-sidecar:<git-sha>` and `sextant-sidecar:latest` (`Makefile:153-157`). The build is **not** wired into `make test`; CI exercises it through a dedicated job.

To verify the image:

```bash
make sidecar-image-test
```

This runs `images/sidecar/test.sh`, which asserts the image builds, every required tool is on PATH (node, npm, git, gh, jq, yq, rg, fzf, curl, wget, make, gcc, python3, go, vim — `images/sidecar/test.sh:42-51`), and the entrypoint binary is present. It also emits a non-fatal warning if the image exceeds 3 GiB (target is `< 2 GiB`).

## Verify the build

```bash
make lint test
```

`make lint` runs three gates (`Makefile:36`): Go (`golangci-lint`), null-pointer analysis (`nilaway`, run separately because it isn't bundled into golangci-lint v2 — `Makefile:43-44`), and TypeScript (`tsc --noEmit`) for both the client and the sidecar entrypoint.

`make test` runs `go test -race -count=1 ./...` plus the TypeScript vitest suites for the client and the sidecar (`Makefile:57-70`).

A faster sanity check that doesn't need the full test suite:

```bash
sextant doctor --preflight
```

Runs only the host-binary checks; useful right after a fresh install or after a laptop reboot to confirm Docker is up.

## Snapshot version reporting

`pkg/version` exposes `GitSHA`, populated via `-ldflags` from the build's `git rev-parse HEAD` (`Makefile:100-101`). `sextant doctor` reads it back to detect stale installed binaries (cross-referenced from `plans/issues/feat-doctor-stale-binary-detection.md`).
```

- [ ] **Step 6.2: Commit**

```bash
git add docs/book/src/getting-started/install.md
git commit -m "$(cat <<'EOF'
docs(book): restructure install.md as automated/manual paths

Adds `make bootstrap` as the lead path with a link to the bootstrap
script source for "what does it actually do". Preserves the existing
dep table and per-dep install commands as the manual fallback for
operators who want to install everything themselves or who are on
Linux. Linux-unverified caveat at the top.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: CLAUDE.md onboarding pointer

**Files:**
- Modify: `CLAUDE.md`

**What this builds:** A short section near the top of `CLAUDE.md` telling future agents where to point users when they ask about onboarding.

### Steps

- [ ] **Step 7.1: Read the current CLAUDE.md to find the right insertion point**

```bash
cat /Users/lena/dev/sextant/CLAUDE.md
```

The section should slot in after the `## Read before deciding anything` block and before `## Build / run / install`, since "onboarding help" is conceptually a triage hint that applies to a wide range of incoming questions.

- [ ] **Step 7.2: Insert the new section**

Use the Edit tool to add this block between the `PRINCIPLES.md` paragraph and the `## Build / run / install` header in `/Users/lena/dev/sextant/CLAUDE.md`:

```markdown

## Helping someone onboard

If the user asks how to get started with sextant, how to install it,
or how to drive it for the first time, point them at:

- `README.md` — the one-page quickstart (operator path on top,
  contributor path below)
- `docs/book/src/getting-started/{install,first-run,repo-tour}.md`
  — the deeper walkthrough (run `mdbook serve docs/book` to browse
  in a browser, or open the `.md` files directly)

Don't reinvent install instructions inline. The mdbook is the source
of truth for the installed-and-running flow; the README is the source
of truth for the quickstart. `make bootstrap` is the canonical
first-command — if someone hits a problem with it, debug there
rather than routing around it.

```

- [ ] **Step 7.3: Verify the file reads cleanly**

```bash
cat /Users/lena/dev/sextant/CLAUDE.md | head -40
```

Expected: the new section appears between `PRINCIPLES.md` and `## Build / run / install`.

- [ ] **Step 7.4: Commit**

```bash
git add CLAUDE.md
git commit -m "$(cat <<'EOF'
docs(CLAUDE): add onboarding-help pointer

Pin future Claude Code agents to the README + mdbook when users ask
about getting started, instead of having them improvise install
instructions inline. References `make bootstrap` as the canonical
first command.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: End-to-end verification

**Files:** none — pure verification.

**What this builds:** Confidence that everything works together before anyone touches a fresh machine.

### Steps

- [ ] **Step 8.1: Run the full lint + test gates**

```bash
cd /Users/lena/dev/sextant
make lint-go
make test-go
shellcheck scripts/bootstrap.sh
```

Expected:
- `make lint-go`: no NEW findings in `cmd/sextant/preflight.go` or `cmd/sextant/doctor.go`. (Per `CLAUDE.md`, ~26 pre-existing issues are tolerated; only files you touched matter.)
- `make test-go`: PASS, including all `TestPreflight_*` and `TestDoctor*`.
- `shellcheck`: no findings.

If lint-go reports issues in your files, fix them (likely candidates: unused imports, missing context propagation, lower-case-init for error strings).

- [ ] **Step 8.2: Exercise the end-to-end bootstrap path**

This host already has every dep, so this is the "happy path, no installs needed" run:

```bash
make bootstrap YES=1
```

Expected output ends with:
```
=== sextant bootstrap plan ===
Package manager: brew
  - make install
  - sextant doctor --preflight
  - sextant init

All dependencies present.
[... make install output ...]
[... sextant doctor --preflight output, all pass ...]
[... sextant init output, mostly "existing" ...]

Bootstrap complete.
Next: sextant start && sextant doctor
```

- [ ] **Step 8.3: Exercise `sextant doctor --preflight` standalone**

```bash
sextant doctor --preflight
sextant doctor --preflight --contributor
```

Expected: green output for both. `--contributor` adds go/node/npm rows.

- [ ] **Step 8.4: Sanity-check that the README quickstart still works**

```bash
sextant start
sextant doctor       # full check; expect all-pass
sextant stop
```

Expected: daemon starts, doctor shows everything green (including the new host-dep rows), daemon stops cleanly.

- [ ] **Step 8.5: Final review of the new docs in browsable form**

```bash
# Optional but recommended if mdbook is installed:
mdbook serve docs/book
# Open http://localhost:3000/getting-started/install.html
```

Skim the install page. Confirm the automated/manual sections render correctly, the link to `scripts/bootstrap.sh` works, and the Linux caveat is visible.

Skim `README.md` rendered on GitHub (push to a fork or use a markdown previewer).

- [ ] **Step 8.6: No commit unless you fixed something in this verification pass.**

If steps 8.1–8.5 surfaced issues, fix them and commit with `fix(...)` messages referencing the specific failure. Otherwise this task closes without a commit.

---

## Self-review checklist

After implementing each task, run through this list one more time before considering the work done:

1. **Spec coverage:** Every section of the spec (`docs/superpowers/specs/2026-05-26-onboarding-and-bootstrap-design.md`) maps to a task above:
   - §1 README rewrite → Task 5
   - §2 CLAUDE.md addition → Task 7
   - §3 Doctor preflight → Tasks 1 + 2
   - §4 `scripts/bootstrap.sh` + Makefile → Tasks 3 + 4
   - §5 mdbook install.md → Task 6
   - §6 Testing → Task 1 (unit tests), Task 8 (end-to-end). Shellcheck per Task 3.4. macOS GH Actions smoke job deferred to a follow-up ticket per spec ("Acceptable to defer this to a follow-up PR").
2. **Placeholder scan:** Each step shows actual code, actual commands, actual expected output. No "TBD" / "handle edge cases" / "add tests for the above".
3. **Type consistency:** `CheckResult` fields (`Kind`, `Check`, `Status`, `Detail`, `Remedy`) match `doctor.go:36-47`. `StatusPass` / `StatusFail` / `StatusWarn` match the existing const block. `lookPathFn` / `dockerInfoFn` are defined in Task 1 and used in the same form in Task 2.
4. **Build-order soundness:** Task 1 makes the helpers compile clean before Task 2 wires them in. Task 3 (bootstrap.sh) can't be smoke-tested end-to-end until Task 2's `--preflight` flag exists. Tasks 5–7 don't add code; they only reference commands that exist by then.

---

## Follow-up tickets (out of scope for this plan)

File these as `plans/issues/` markdown files once the main work merges:

- **`feat-bootstrap-linux-apt-parity.md`** — full Linux automation, possibly with explicit apt-repo bootstrap for nats-server and clickhouse.
- **`feat-bootstrap-ci-smoke.md`** — GitHub Actions macOS smoke job: fresh runner, `make bootstrap YES=1`, assert `sextant doctor` exits 0. Gated as a separate job like `sidecar-image`.
- **`feat-doctor-preflight-json.md`** — `sextant doctor --preflight --json` for machine consumption (mirrors existing `--json` pattern).
- **`docs-mdbook-getting-started-restructure.md`** — the rest of `getting-started/` and `operator-guide/` could use similar layered treatment.

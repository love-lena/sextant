package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/version"
)

func TestDoctorAgainstFreshInit(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)

	// Expect: every static check (config, ca, operator-creds, clickhouse-password,
	// templates, data-dirs) passes; daemon check is "not-running".
	var failures, notRunning int
	wantPass := map[string]bool{
		"config":              true,
		"ca":                  true,
		"operator-creds":      true,
		"clickhouse-password": true,
		"templates":           true,
	}
	seen := map[string]bool{}
	for _, r := range results {
		if r.Kind == "host-dep" {
			continue // environmental; not what this test exercises
		}
		if r.Status == StatusFail {
			failures++
		}
		if r.Status == StatusNotRunning {
			notRunning++
		}
		if wantPass[r.Kind] {
			if r.Status != StatusPass {
				t.Errorf("kind %s status = %s, want pass: %s", r.Kind, r.Status, r.Detail)
			}
			seen[r.Kind] = true
		}
	}
	for k := range wantPass {
		if !seen[k] {
			t.Errorf("missing check %s in doctor output", k)
		}
	}
	if failures != 0 {
		t.Errorf("expected zero failures, got %d (%+v)", failures, results)
	}
	// Two not-running rows on a fresh init without a started daemon:
	// the existing `daemon` row plus the `version`/daemon row added by
	// the get_version surface — both surface the "no daemon yet" state
	// with a start-daemon remedy.
	if notRunning != 2 {
		t.Errorf("expected exactly two 'not-running' rows (daemon + version/daemon), got %d", notRunning)
	}
}

func TestDoctorReportsCorruptedCA(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	// Corrupt the CA private key.
	if err := os.WriteFile(filepath.Join(opts.ConfigDir, "ca.key"), []byte("not a real key"), 0o600); err != nil {
		t.Fatalf("corrupt ca.key: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)
	var caRow *CheckResult
	for i := range results {
		if results[i].Kind == "ca" {
			caRow = &results[i]
		}
	}
	if caRow == nil {
		t.Fatal("no ca row in results")
	}
	if caRow.Status != StatusFail {
		t.Errorf("ca status = %s, want fail", caRow.Status)
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)
	var out bytes.Buffer
	emit(&out, results, true)
	var parsed []CheckResult
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("emit JSON output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(parsed) != len(results) {
		t.Errorf("parsed %d rows, want %d", len(parsed), len(results))
	}
}

// TestDoctorFlagsStaleBinary covers feat-doctor-stale-binary-detection:
// when the binary's embedded GitSHA lags the workspace HEAD, the
// binary-version check returns warn with "behind" in the detail.
func TestDoctorFlagsStaleBinary(t *testing.T) {
	repoRoot := stubGitRepo(t)
	firstSHA := gitRunOut(t, repoRoot, "rev-parse", "HEAD")
	// Advance HEAD with a second commit so firstSHA is now an ancestor.
	if err := os.WriteFile(filepath.Join(repoRoot, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-q", "-m", "second")

	r, ok := checkBinaryVersion(repoRoot, firstSHA)
	if !ok {
		t.Fatal("checkBinaryVersion returned skip; want emitted result")
	}
	if r.Kind != "binary-version" {
		t.Errorf("kind = %q, want binary-version", r.Kind)
	}
	if r.Status != StatusWarn {
		t.Errorf("status = %q, want warn (detail: %s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "behind") {
		t.Errorf("detail = %q, want substring 'behind'", r.Detail)
	}
}

// TestDoctorFlagsStaleBinarySkipsWhenUnconfigured covers the silent-skip
// path: when no SHA is embedded OR no repoRoot is set, the check emits
// nothing rather than failing.
func TestDoctorFlagsStaleBinarySkipsWhenUnconfigured(t *testing.T) {
	if _, ok := checkBinaryVersion("", "abc"); ok {
		t.Error("expected skip when repoRoot is empty")
	}
	if _, ok := checkBinaryVersion(t.TempDir(), ""); ok {
		t.Error("expected skip when installed SHA is empty")
	}
	// Real path that isn't a git checkout — skip silently rather than warn.
	if _, ok := checkBinaryVersion(t.TempDir(), "deadbeef"); ok {
		t.Error("expected skip when repoRoot is not a git checkout")
	}
}

// TestDoctorFlagsStaleBinaryReadsVersionPackage exercises the
// collectChecks integration path: setting pkg/version.GitSHA and pointing
// cfg.Worktree.RepoRoot at a stub repo should surface the warn row.
func TestDoctorFlagsStaleBinaryReadsVersionPackage(t *testing.T) {
	repoRoot := stubGitRepo(t)
	firstSHA := gitRunOut(t, repoRoot, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repoRoot, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-q", "-m", "second")

	prev := version.GitSHA
	version.GitSHA = firstSHA
	t.Cleanup(func() { version.GitSHA = prev })

	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	// Patch the sextantd.toml to set worktree.repo_root.
	patchRepoRoot(t, filepath.Join(opts.ConfigDir, "sextantd.toml"), repoRoot)

	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)
	var row *CheckResult
	for i := range results {
		if results[i].Kind == "binary-version" {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatalf("no binary-version row in %+v", results)
	}
	if row.Status != StatusWarn || !strings.Contains(row.Detail, "behind") {
		t.Errorf("binary-version row = %+v; want warn with 'behind'", row)
	}
}

// TestDoctorFlagsWorkingTreeDrift covers
// bug-worktree-merge-leaves-operator-checkout-stale: after the workspace's
// HEAD ref is advanced externally (simulating worktree_merge), the working
// tree differs from HEAD. The working-tree check should warn.
func TestDoctorFlagsWorkingTreeDrift(t *testing.T) {
	repoRoot := stubGitRepo(t)
	// External modification simulates the post-merge drift: HEAD has
	// moved (or, equivalently, the working tree didn't follow). For
	// determinism we modify a tracked file directly.
	if err := os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("modified-out-of-band"), 0o644); err != nil {
		t.Fatalf("modify a.txt: %v", err)
	}

	r, ok := checkWorkingTree(repoRoot)
	if !ok {
		t.Fatal("checkWorkingTree returned skip; want emitted result")
	}
	if r.Kind != "working-tree" {
		t.Errorf("kind = %q, want working-tree", r.Kind)
	}
	if r.Status != StatusWarn {
		t.Errorf("status = %q, want warn (detail: %s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "differ from HEAD") {
		t.Errorf("detail = %q, want substring 'differ from HEAD'", r.Detail)
	}
	if !strings.Contains(r.Detail, "git checkout HEAD -- .") {
		t.Errorf("detail = %q, want the recovery hint", r.Detail)
	}
}

// TestDoctorWorkingTreeCleanPasses ensures the check returns pass — not
// warn — when the working tree matches HEAD. Guards against the check
// becoming a false-positive nag.
func TestDoctorWorkingTreeCleanPasses(t *testing.T) {
	repoRoot := stubGitRepo(t)
	r, ok := checkWorkingTree(repoRoot)
	if !ok {
		t.Fatal("checkWorkingTree returned skip on a real git repo")
	}
	if r.Status != StatusPass {
		t.Errorf("clean working tree status = %q, want pass (detail: %s)", r.Status, r.Detail)
	}
}

// TestDoctorWorkingTreeSkipsWhenUnconfigured matches the binary-version
// skip behavior so an operator without `worktree.repo_root` set doesn't
// see a phantom row.
func TestDoctorWorkingTreeSkipsWhenUnconfigured(t *testing.T) {
	if _, ok := checkWorkingTree(""); ok {
		t.Error("expected skip when repoRoot is empty")
	}
	if _, ok := checkWorkingTree(t.TempDir()); ok {
		t.Error("expected skip when repoRoot is not a git checkout")
	}
}

// stubGitRepo initialises a minimal git repo with one tracked file and
// one commit. Returns the repo path.
func stubGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitRunOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// patchRepoRoot rewrites the [worktree] block in a sextantd.toml so the
// integration tests can point doctor at a stub repo.
func patchRepoRoot(t *testing.T, tomlPath, repoRoot string) {
	t.Helper()
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("read %s: %v", tomlPath, err)
	}
	// The TOML serialiser may pick either quoting style; cover both.
	updated := strings.Replace(string(raw), `repo_root = ""`, `repo_root = "`+repoRoot+`"`, 1)
	if updated == string(raw) {
		updated = strings.Replace(string(raw), `repo_root = ''`, `repo_root = '`+repoRoot+`'`, 1)
	}
	if updated == string(raw) {
		t.Fatalf("repo_root line not found in %s; sample:\n%s", tomlPath, raw)
	}
	if err := os.WriteFile(tomlPath, []byte(updated), 0o600); err != nil {
		t.Fatalf("write %s: %v", tomlPath, err)
	}
}

func TestDoctorFailsOnMissingConfig(t *testing.T) {
	dir := t.TempDir()
	results := collectChecks(context.Background(), filepath.Join(dir, "cfg"), filepath.Join(dir, "data"), false)
	hasFail := false
	for _, r := range results {
		if r.Status == StatusFail {
			hasFail = true
		}
	}
	if !hasFail {
		t.Error("expected at least one fail row when nothing is initialized")
	}
}

// TestDoctor_DaemonNotRunning_HasRemedy ensures the not-running daemon row
// carries the obvious remedy. The daemon row reaches the not-running state
// after a fresh init when no daemon has been started.
func TestDoctor_DaemonNotRunning_HasRemedy(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)
	var row *CheckResult
	for i := range results {
		if results[i].Kind == "daemon" && results[i].Status == StatusNotRunning {
			row = &results[i]
		}
	}
	if row == nil {
		t.Fatalf("no daemon not-running row in %+v", results)
	}
	const want = "start the daemon: sextant start"
	if row.Remedy != want {
		t.Errorf("daemon not-running remedy = %q, want %q", row.Remedy, want)
	}
}

// TestDoctor_BinaryBehind_HasRemedy reuses the stale-binary harness from
// TestDoctorFlagsStaleBinary and asserts the warn row carries the obvious
// remedy ("refresh installed binary: make install").
func TestDoctor_BinaryBehind_HasRemedy(t *testing.T) {
	repoRoot := stubGitRepo(t)
	firstSHA := gitRunOut(t, repoRoot, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repoRoot, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-q", "-m", "second")

	r, ok := checkBinaryVersion(repoRoot, firstSHA)
	if !ok {
		t.Fatal("checkBinaryVersion returned skip; want emitted result")
	}
	const want = "refresh installed binary: make install"
	if r.Remedy != want {
		t.Errorf("binary-version behind remedy = %q, want %q", r.Remedy, want)
	}
}

// TestDoctor_HumanOutput_RendersRemedyLine ensures the human-readable
// emit() output appends an indented "→ <remedy>" line under each row that
// has a remedy populated.
func TestDoctor_HumanOutput_RendersRemedyLine(t *testing.T) {
	results := []CheckResult{
		{
			Kind:   "daemon",
			Check:  "/tmp/runtime.json",
			Status: StatusNotRunning,
			Detail: "runtime.json not present (daemon not started)",
			Remedy: "start the daemon: sextant start",
		},
	}
	var out bytes.Buffer
	emit(&out, results, false)
	got := out.String()
	if !strings.Contains(got, "start the daemon: sextant start") {
		t.Errorf("human output missing remedy text:\n%s", got)
	}
	if !strings.Contains(got, "→") {
		t.Errorf("human output missing arrow indicator:\n%s", got)
	}
}

// TestDoctor_JSONOutput_HasRemedyField checks that --json emits the
// "remedy" key for rows that carry a remedy, and omits it (via omitempty)
// for rows that don't.
func TestDoctor_JSONOutput_HasRemedyField(t *testing.T) {
	results := []CheckResult{
		{
			Kind:   "daemon",
			Check:  "/tmp/runtime.json",
			Status: StatusNotRunning,
			Detail: "runtime.json not present (daemon not started)",
			Remedy: "start the daemon: sextant start",
		},
		{
			Kind:   "config",
			Check:  "/tmp/sextantd.toml",
			Status: StatusPass,
			Detail: "loaded",
		},
	}
	var out bytes.Buffer
	emit(&out, results, true)
	raw := out.String()
	if !strings.Contains(raw, `"remedy"`) {
		t.Errorf("JSON output missing remedy field:\n%s", raw)
	}
	if !strings.Contains(raw, "start the daemon: sextant start") {
		t.Errorf("JSON output missing remedy text:\n%s", raw)
	}
	// Round-trip and ensure the passing config row didn't gain a remedy.
	var parsed []CheckResult
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	for _, r := range parsed {
		if r.Kind == "config" && r.Remedy != "" {
			t.Errorf("passing config row got remedy %q (omitempty broken)", r.Remedy)
		}
	}
}

// TestDoctorPreflightReturnsOnlyHostDepRows exercises the --preflight
// dispatch path: collectHostDepChecks must return only "host-dep" rows and
// must return at least one of them.
func TestDoctorPreflightReturnsOnlyHostDepRows(t *testing.T) {
	// Hermetic: every dep "present" via fake lookup; docker daemon OK.
	lookFn := fakeLookup("nats-server", "clickhouse", "docker", "go", "node", "npm")
	results := collectHostDepChecks(context.Background(), false, lookFn, okDocker, fakeGoVersion("1.26.1"))

	if len(results) == 0 {
		t.Fatal("expected at least one host-dep row, got zero")
	}
	for _, r := range results {
		if r.Kind != "host-dep" {
			t.Errorf("preflight returned non-host-dep row: kind=%s check=%s", r.Kind, r.Check)
		}
	}
}

// TestDoctorFormatBuildLine pins the version cell shape — operators and
// scripts grep on this format. CLI row (no startedAt) shows
// "<ver> (sha <short>)"; daemon row adds the pid + RFC3339 start time.
func TestDoctorFormatBuildLine(t *testing.T) {
	cliLine := formatBuildLine("v0.2.0", "abc1234", 0, time.Time{})
	if cliLine != "v0.2.0 (sha abc1234)" {
		t.Errorf("cli line = %q, want %q", cliLine, "v0.2.0 (sha abc1234)")
	}
	started := time.Date(2026, 5, 28, 10, 32, 11, 0, time.UTC)
	daemonLine := formatBuildLine("v0.2.0", "abc1234", 12345, started)
	want := "v0.2.0 (sha abc1234, pid 12345, started 2026-05-28T10:32:11Z)"
	if daemonLine != want {
		t.Errorf("daemon line = %q, want %q", daemonLine, want)
	}
	// Empty commit falls back to "unknown" so a binary built without
	// -ldflags still renders a meaningful row.
	emptyCommitLine := formatBuildLine("dev", "", 0, time.Time{})
	if emptyCommitLine != "dev (sha unknown)" {
		t.Errorf("empty-commit line = %q, want %q", emptyCommitLine, "dev (sha unknown)")
	}
}

// TestDoctorVersionMismatch covers the warn-with-remedy path the issue
// (`plans/issues/feat-doctor-show-daemon-version.md`) specifies: a CLI
// running newer than the daemon (the common shape after `make install`
// without `daemon restart`) prints the mismatch warning that names both
// versions and tells the operator what to run.
func TestDoctorVersionMismatch(t *testing.T) {
	row, mismatch := versionMismatch("v0.2.0", "v0.1.7")
	if !mismatch {
		t.Fatal("versionMismatch returned false for differing versions")
	}
	if row.Status != StatusWarn {
		t.Errorf("status = %q, want warn", row.Status)
	}
	if row.Kind != "version" || row.Check != "mismatch" {
		t.Errorf("kind/check = %q/%q, want version/mismatch", row.Kind, row.Check)
	}
	if !strings.Contains(row.Detail, "CLI v0.2.0") || !strings.Contains(row.Detail, "daemon v0.1.7") {
		t.Errorf("detail %q missing version pair", row.Detail)
	}
	if !strings.Contains(row.Detail, "sextant daemon restart") {
		t.Errorf("detail %q missing remedy hint", row.Detail)
	}
	if row.Remedy == "" {
		t.Error("remedy must be populated so emit() renders the arrow line")
	}
}

// TestDoctorVersionMatchEmitsNoWarning — when CLI and daemon report the
// same version, versionMismatch returns (zero, false) so the doctor
// table stays quiet. Guards against the warning becoming a permanent nag.
func TestDoctorVersionMatchEmitsNoWarning(t *testing.T) {
	row, mismatch := versionMismatch("v0.2.0", "v0.2.0")
	if mismatch {
		t.Errorf("versionMismatch returned true for matching versions; row=%+v", row)
	}
}

// TestDoctorCollectVersionChecksOfflineEmitsCLIOnly — when the daemon
// isn't reachable (no runtime.json) collectVersionChecks still emits
// the CLI row, plus a daemon-not-running row with the start-daemon
// remedy. No mismatch warning when we can't query.
func TestDoctorCollectVersionChecksOfflineEmitsCLIOnly(t *testing.T) {
	rows := collectVersionChecks(context.Background(), false)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (cli + daemon-not-running)", len(rows))
	}
	if rows[0].Kind != "version" || rows[0].Check != "cli" || rows[0].Status != StatusPass {
		t.Errorf("CLI row = %+v", rows[0])
	}
	if rows[1].Kind != "version" || rows[1].Check != "daemon" || rows[1].Status != StatusNotRunning {
		t.Errorf("daemon row = %+v", rows[1])
	}
	if rows[1].Remedy != remedyStartDaemon {
		t.Errorf("daemon row remedy = %q, want %q", rows[1].Remedy, remedyStartDaemon)
	}
}

// TestDoctor_PassingCheck_NoRemedy ensures passing checks don't carry
// remedies in either format. A passing row with a remedy would be a UX
// regression — operators shouldn't see "fix it" advice next to "all good".
func TestDoctor_PassingCheck_NoRemedy(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir, false)
	for _, r := range results {
		if r.Status == StatusPass && r.Remedy != "" {
			t.Errorf("passing check %s/%s carried remedy %q", r.Kind, r.Check, r.Remedy)
		}
	}
	// Also verify human output doesn't render arrow lines under passing rows.
	var out bytes.Buffer
	emit(&out, results, false)
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "→") && strings.Contains(line, "pass") {
			t.Errorf("passing row rendered remedy line: %q", line)
		}
	}
}

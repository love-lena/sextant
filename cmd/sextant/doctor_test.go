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

	"github.com/love-lena/sextant-initial/pkg/version"
)

func TestDoctorAgainstFreshInit(t *testing.T) {
	opts := tempInitOpts(t)
	var buf bytes.Buffer
	if err := doInit(context.Background(), &buf, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)

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
	if notRunning != 1 {
		t.Errorf("expected exactly one 'not-running' row, got %d", notRunning)
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
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)
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
	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)
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

	results := collectChecks(context.Background(), opts.ConfigDir, opts.DataDir)
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
	results := collectChecks(context.Background(), filepath.Join(dir, "cfg"), filepath.Join(dir, "data"))
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

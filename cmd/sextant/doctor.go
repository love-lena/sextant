package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/version"
)

// CheckStatus enumerates a check's outcome.
type CheckStatus string

const (
	StatusPass       CheckStatus = "pass"
	StatusFail       CheckStatus = "fail"
	StatusNotRunning CheckStatus = "not-running"
	StatusWarn       CheckStatus = "warn"
)

// CheckResult is one row in doctor's report.
//
// Remedy is the obvious next action when a check fails or warns and the
// answer is mechanical (e.g. "make install", "run sextant init"). Left
// blank when the failure mode needs operator judgment — doctor shouldn't
// invent vague advice like "look at logs".
type CheckResult struct {
	Kind   string      `json:"kind"`
	Check  string      `json:"check"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
	Remedy string      `json:"remedy,omitempty"`
}

// Remedy strings — kept as package-level consts so tests and the rendering
// code share the exact text, and grep can find every site that wires one
// up.
const (
	remedyStartDaemon       = "start the daemon: sextant start"
	remedyMakeInstall       = "refresh installed binary: make install"
	remedyCommitThenInstall = "commit/stash changes, then make install"
	remedyRunInit           = "run sextant init"
	remedyRestartForStream  = "restart sextantd to re-run Bootstrap()"
	remedyRestartForMigrate = "restart sextantd to re-run migrations"
)

var errDoctorFailures = errors.New("doctor: one or more checks failed")

func isDoctorFailureErr(err error) bool { return errors.Is(err, errDoctorFailures) }

// newDoctorCmd wires `sextant doctor`. Doctor is a top-level singleton
// per `feat-cli-resource-verb-cleanup` — verb on the sextant install
// itself, diagnosing the install's health.
func newDoctorCmd() *cobra.Command {
	var preflight, contributor bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health diagnostics for sextantd, NATS, ClickHouse, config",
		Long: `Runs health diagnostics against the installation rooted at the given
config and data dirs (defaults: ~/.config/sextant, ~/.local/share/sextant).

--preflight runs only host-dep checks (nats-server, clickhouse, docker
binary + daemon) and skips anything that needs config to exist. Use it
before sextant init has ever been run, or from scripts/bootstrap.sh.

--contributor additionally checks deps needed to build sextant from
source (go, node, npm). Off by default; operators using installed
binaries don't need it.

Exit code 0 on all-pass (or only "not running" warnings), 2 on failure.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfgDir, dataDirAbs, err := resolveInitPaths(globalFlags.configDir, globalFlags.dataDir)
			if err != nil {
				return err
			}
			var results []CheckResult
			if preflight {
				results = collectHostDepChecks(ctx, contributor, exec.LookPath, defaultDockerInfo, defaultRunCmd)
			} else {
				results = collectChecks(ctx, cfgDir, dataDirAbs, contributor)
			}
			failed := emit(cmd.OutOrStdout(), results, globalFlags.asJSON)
			if failed {
				return errDoctorFailures
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&preflight, "preflight", false,
		"host-dep checks only (skips config, daemon, NATS, ClickHouse)")
	cmd.Flags().BoolVar(&contributor, "contributor", false,
		"additionally check contributor deps (go, node, npm)")
	return cmd
}

// collectChecks runs every diagnostic and returns the rows in display
// order. We try to keep each check side-effect-free.
func collectChecks(ctx context.Context, cfgDir, dataDir string, contributor bool) []CheckResult {
	var out []CheckResult
	out = append(out, collectHostDepChecks(ctx, contributor, exec.LookPath, defaultDockerInfo, defaultRunCmd)...)

	sextantdTomlPath := filepath.Join(cfgDir, "sextantd.toml")
	cfg, cfgErr := sextantd.LoadConfig(sextantdTomlPath)
	if cfgErr != nil {
		out = append(out, CheckResult{
			Kind: "config", Check: sextantdTomlPath,
			Status: StatusFail, Detail: cfgErr.Error(),
			Remedy: remedyRunInit,
		})
		// Without config, every downstream check would need fallback
		// paths. Use defaults so the rest of the report is still useful.
		cfg = sextantd.DefaultConfig(cfgDir, dataDir)
	} else {
		out = append(out, CheckResult{
			Kind: "config", Check: sextantdTomlPath, Status: StatusPass,
			Detail: "loaded",
		})
	}

	out = append(out, checkCA(cfg.CA.KeyPath, cfg.CA.PubPath))
	out = append(out, checkOperatorCreds(cfg.NATS.OperatorCreds))
	out = append(out, checkClickHousePassword(cfg.ClickHouse.PasswordFile))
	out = append(out, checkTemplates(cfg.Paths.TemplatesDir))
	out = append(out, checkDataDirs(cfg)...)

	// Workspace-aware checks: only emitted when cfg.Worktree.RepoRoot is
	// set AND points at a real git checkout. Both are operator-checkout
	// drift detectors (issues: feat-doctor-stale-binary-detection,
	// bug-worktree-merge-leaves-operator-checkout-stale).
	if r, ok := checkBinaryVersion(cfg.Worktree.RepoRoot, version.GitSHA); ok {
		out = append(out, r)
	}
	if r, ok := checkWorkingTree(cfg.Worktree.RepoRoot); ok {
		out = append(out, r)
	}

	runtime, runtimeErr := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	switch {
	case runtimeErr == nil:
		out = append(out, CheckResult{
			Kind: "daemon", Check: cfg.Paths.RuntimeFile,
			Status: StatusPass,
			Detail: fmt.Sprintf("pid=%d started=%s", runtime.PID, runtime.StartedAt.Format(time.RFC3339)),
		})
		out = append(out, checkControlSocket(cfg.Daemon.ControlSocket))
		out = append(out, checkNATS(ctx, runtime.NATSAddr))
		out = append(out, checkClickHouseAddr(runtime.ClickHouseTCP))
	case errors.Is(runtimeErr, os.ErrNotExist) || strings.Contains(runtimeErr.Error(), "no such file"):
		out = append(out, CheckResult{
			Kind: "daemon", Check: cfg.Paths.RuntimeFile,
			Status: StatusNotRunning, Detail: "runtime.json not present (daemon not started)",
			Remedy: remedyStartDaemon,
		})
	default:
		out = append(out, CheckResult{
			Kind: "daemon", Check: cfg.Paths.RuntimeFile,
			Status: StatusFail, Detail: runtimeErr.Error(),
		})
	}
	return out
}

func checkCA(keyPath, pubPath string) CheckResult {
	st, err := os.Stat(keyPath)
	switch {
	case err != nil:
		return CheckResult{
			Kind: "ca", Check: keyPath, Status: StatusFail, Detail: err.Error(),
			Remedy: remedyRunInit,
		}
	case st.Mode().Perm() != 0o600:
		return CheckResult{
			Kind: "ca", Check: keyPath, Status: StatusFail,
			Detail: fmt.Sprintf("mode %o, want 0600", st.Mode().Perm()),
		}
	}
	if _, err := authjwt.LoadCA(keyPath, pubPath); err != nil {
		return CheckResult{
			Kind: "ca", Check: keyPath, Status: StatusFail, Detail: err.Error(),
			Remedy: remedyRunInit,
		}
	}
	return CheckResult{Kind: "ca", Check: keyPath, Status: StatusPass, Detail: "ed25519 keypair valid"}
}

func checkOperatorCreds(path string) CheckResult {
	st, err := os.Stat(path)
	if err != nil {
		return CheckResult{
			Kind: "operator-creds", Check: path, Status: StatusFail, Detail: err.Error(),
			Remedy: remedyRunInit,
		}
	}
	if st.Mode().Perm() != 0o600 {
		return CheckResult{
			Kind: "operator-creds", Check: path, Status: StatusFail,
			Detail: fmt.Sprintf("mode %o, want 0600", st.Mode().Perm()),
		}
	}
	if _, err := sextantd.ReadOperatorCreds(path); err != nil {
		return CheckResult{
			Kind: "operator-creds", Check: path, Status: StatusFail, Detail: err.Error(),
			Remedy: remedyRunInit,
		}
	}
	return CheckResult{Kind: "operator-creds", Check: path, Status: StatusPass, Detail: "loaded"}
}

func checkClickHousePassword(path string) CheckResult {
	st, err := os.Stat(path)
	if err != nil {
		return CheckResult{
			Kind: "clickhouse-password", Check: path, Status: StatusFail, Detail: err.Error(),
			Remedy: remedyRunInit,
		}
	}
	if st.Mode().Perm() != 0o600 {
		return CheckResult{
			Kind: "clickhouse-password", Check: path, Status: StatusFail,
			Detail: fmt.Sprintf("mode %o, want 0600", st.Mode().Perm()),
		}
	}
	if _, err := sextantd.ReadPasswordFile(path); err != nil {
		return CheckResult{
			Kind: "clickhouse-password", Check: path, Status: StatusFail, Detail: err.Error(),
			Remedy: remedyRunInit,
		}
	}
	return CheckResult{Kind: "clickhouse-password", Check: path, Status: StatusPass, Detail: "loaded"}
}

func checkTemplates(dir string) CheckResult {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return CheckResult{Kind: "templates", Check: dir, Status: StatusFail, Detail: err.Error()}
	}
	hasDefault := false
	for _, e := range entries {
		if !e.IsDir() && e.Name() == "default.toml" {
			hasDefault = true
		}
	}
	if !hasDefault {
		return CheckResult{Kind: "templates", Check: dir, Status: StatusFail, Detail: "default.toml missing"}
	}
	return CheckResult{
		Kind: "templates", Check: dir, Status: StatusPass,
		Detail: fmt.Sprintf("%d file(s)", len(entries)),
	}
}

func checkDataDirs(cfg sextantd.Config) []CheckResult {
	type dirCheck struct {
		label string
		path  string
	}
	dirs := []dirCheck{
		{"nats", cfg.NATS.DataDir},
		{"clickhouse", cfg.ClickHouse.DataDir},
		{"shipper-buffer", filepath.Join(cfg.Paths.DataDir, "shipper-buffer")},
		{"test", filepath.Join(cfg.Paths.DataDir, "test")},
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].label < dirs[j].label })
	out := make([]CheckResult, 0, len(dirs))
	for _, d := range dirs {
		st, err := os.Stat(d.path)
		switch {
		case err != nil:
			out = append(out, CheckResult{Kind: "data-dir", Check: d.label, Status: StatusFail, Detail: err.Error()})
		case !st.IsDir():
			out = append(out, CheckResult{Kind: "data-dir", Check: d.label, Status: StatusFail, Detail: "not a directory"})
		default:
			out = append(out, CheckResult{Kind: "data-dir", Check: d.label, Status: StatusPass, Detail: d.path})
		}
	}
	return out
}

func checkControlSocket(path string) CheckResult {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return CheckResult{Kind: "control-socket", Check: path, Status: StatusFail, Detail: err.Error()}
	}
	defer conn.Close() //nolint:errcheck // best-effort close
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return CheckResult{Kind: "control-socket", Check: path, Status: StatusFail, Detail: err.Error()}
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return CheckResult{Kind: "control-socket", Check: path, Status: StatusFail, Detail: err.Error()}
	}
	greeting := strings.TrimSpace(string(buf[:n]))
	if !strings.HasPrefix(greeting, "OK") {
		return CheckResult{
			Kind: "control-socket", Check: path, Status: StatusFail,
			Detail: fmt.Sprintf("unexpected greeting: %q", greeting),
		}
	}
	return CheckResult{Kind: "control-socket", Check: path, Status: StatusPass, Detail: greeting}
}

func checkNATS(_ context.Context, addr string) CheckResult {
	if addr == "" {
		return CheckResult{Kind: "nats", Check: "(unset)", Status: StatusFail, Detail: "runtime.json has no nats_addr"}
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return CheckResult{Kind: "nats", Check: addr, Status: StatusFail, Detail: err.Error()}
	}
	_ = conn.Close()
	return CheckResult{Kind: "nats", Check: addr, Status: StatusPass, Detail: "tcp reachable"}
}

func checkClickHouseAddr(addr string) CheckResult {
	if addr == "" {
		return CheckResult{
			Kind: "clickhouse", Check: "(unset)", Status: StatusFail,
			Detail: "runtime.json has no clickhouse_tcp",
		}
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return CheckResult{Kind: "clickhouse", Check: addr, Status: StatusFail, Detail: err.Error()}
	}
	_ = conn.Close()
	return CheckResult{Kind: "clickhouse", Check: addr, Status: StatusPass, Detail: "tcp reachable"}
}

// checkBinaryVersion compares the binary's ldflags-embedded git SHA with
// the workspace HEAD at repoRoot. It returns (result, true) when something
// useful can be reported and (zero, false) when the check should be omitted
// silently — either because no SHA was embedded (binary built without
// -ldflags), no workspace root is configured, or repoRoot isn't a git
// checkout. The check is warn-only: an operator may deliberately run an
// older binary, so we never escalate to fail.
func checkBinaryVersion(repoRoot, installedSHA string) (CheckResult, bool) {
	if installedSHA == "" || repoRoot == "" {
		return CheckResult{}, false
	}
	headSHA, err := gitRevParseHEAD(repoRoot)
	if err != nil {
		// Not a git checkout (or git missing) — skip silently rather
		// than failing a check the operator can't act on.
		return CheckResult{}, false
	}
	if headSHA == installedSHA {
		return CheckResult{
			Kind: "binary-version", Check: repoRoot, Status: StatusPass,
			Detail: fmt.Sprintf("installed binary matches HEAD %s", shortSHA(headSHA)),
		}, true
	}
	// Is the installed SHA an ancestor of HEAD? If yes, count how far
	// behind; if no, the operator built from a different branch.
	ahead, ancestor := gitAheadCount(repoRoot, installedSHA, headSHA)
	if !ancestor {
		return CheckResult{
			Kind: "binary-version", Check: repoRoot, Status: StatusWarn,
			Detail: fmt.Sprintf("installed %s is not in ancestry of workspace HEAD %s; consider `make install`",
				shortSHA(installedSHA), shortSHA(headSHA)),
			Remedy: remedyMakeInstall,
		}, true
	}
	return CheckResult{
		Kind: "binary-version", Check: repoRoot, Status: StatusWarn,
		Detail: fmt.Sprintf("installed binary is %d commits behind workspace HEAD (%s → %s); consider `make install`",
			ahead, shortSHA(installedSHA), shortSHA(headSHA)),
		Remedy: remedyMakeInstall,
	}, true
}

// checkWorkingTree warns when the workspace at repoRoot has tracked files
// that differ from HEAD. The driving case is `worktree_merge` advancing
// main externally to the operator's checkout (issue: bug-worktree-merge-
// leaves-operator-checkout-stale): the ref moves but the working tree
// doesn't, leaving `git status` showing apparent edits the operator never
// made. The fix is a single `git checkout HEAD -- .` and this check tells
// the operator that's what they need.
func checkWorkingTree(repoRoot string) (CheckResult, bool) {
	if repoRoot == "" {
		return CheckResult{}, false
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		return CheckResult{}, false
	}
	cmd := exec.Command("git", "-C", repoRoot, "diff", "--name-only", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Kind: "working-tree", Check: repoRoot, Status: StatusWarn,
			Detail: fmt.Sprintf("git diff failed: %v", err),
		}, true
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return CheckResult{
			Kind: "working-tree", Check: repoRoot, Status: StatusPass,
			Detail: "working tree matches HEAD",
		}, true
	}
	n := strings.Count(trimmed, "\n") + 1
	return CheckResult{
		Kind: "working-tree", Check: repoRoot, Status: StatusWarn,
		Detail: fmt.Sprintf("%d files differ from HEAD; run `git checkout HEAD -- .` to sync", n),
		Remedy: remedyCommitThenInstall,
	}, true
}

// gitRevParseHEAD returns the full SHA of HEAD in repoRoot.
func gitRevParseHEAD(repoRoot string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitAheadCount returns the number of commits in repoRoot reachable from
// tip but not from base, and whether base is actually an ancestor of tip.
// When ancestor is false, the returned count is meaningless.
func gitAheadCount(repoRoot, base, tip string) (count int, ancestor bool) {
	if err := exec.Command("git", "-C", repoRoot, "merge-base", "--is-ancestor", base, tip).Run(); err != nil {
		return 0, false
	}
	out, err := exec.Command("git", "-C", repoRoot, "rev-list", "--count", base+".."+tip).Output()
	if err != nil {
		return 0, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, true
	}
	return n, true
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// emit prints the results in either tabular or JSON form. Returns true
// if any row failed (StatusFail; StatusNotRunning is treated as a warning,
// not a failure).
func emit(w io.Writer, results []CheckResult, asJSON bool) bool {
	failed := false
	for _, r := range results {
		if r.Status == StatusFail {
			failed = true
		}
	}
	if asJSON {
		buf, _ := json.MarshalIndent(results, "", "  ")
		println(w, string(buf))
		return failed
	}
	// Column widths: keep small + readable.
	maxKind, maxCheck, maxStatus := 0, 0, 0
	for _, r := range results {
		if len(r.Kind) > maxKind {
			maxKind = len(r.Kind)
		}
		if len(r.Check) > maxCheck {
			maxCheck = len(r.Check)
		}
		if len(string(r.Status)) > maxStatus {
			maxStatus = len(string(r.Status))
		}
	}
	for _, r := range results {
		printf(w, "%-*s  %-*s  %-*s  %s\n",
			maxKind, r.Kind,
			maxCheck, truncate(r.Check, 60),
			maxStatus, string(r.Status),
			r.Detail,
		)
		// Indented remedy line under the row, only when populated.
		// Indent matches the second column so the arrow lines up under
		// the check name visually. Passing rows never carry a remedy
		// (we only set it on fail/warn/not-running paths) so this stays
		// silent on the happy path.
		if r.Remedy != "" {
			printf(w, "%-*s    → %s\n", maxKind, "", r.Remedy)
		}
	}
	return failed
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return "..." + s[len(s)-n+3:]
}

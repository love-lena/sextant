package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
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
type CheckResult struct {
	Kind   string      `json:"kind"`
	Check  string      `json:"check"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

var errDoctorFailures = errors.New("doctor: one or more checks failed")

func isDoctorFailureErr(err error) bool { return errors.Is(err, errDoctorFailures) }

func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	asJSON := fs.Bool("json", false, "emit machine-parseable JSON")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(doctorUsage)
		return nil
	}

	cfgDir, dataDirAbs, err := resolveInitPaths(*configDir, *dataDir)
	if err != nil {
		return err
	}

	results := collectChecks(ctx, cfgDir, dataDirAbs)
	failed := emit(os.Stdout, results, *asJSON)
	if failed {
		return errDoctorFailures
	}
	return nil
}

const doctorUsage = `usage: sextant doctor [--config-dir PATH] [--data-dir PATH] [--json]

Runs health diagnostics against the installation rooted at the given
config and data dirs (defaults: ~/.config/sextant, ~/.local/share/sextant).

Exit code 0 on all-pass (or only "not running" warnings), 2 on failure.`

// collectChecks runs every diagnostic and returns the rows in display
// order. We try to keep each check side-effect-free.
func collectChecks(ctx context.Context, cfgDir, dataDir string) []CheckResult {
	var out []CheckResult

	sextantdTomlPath := filepath.Join(cfgDir, "sextantd.toml")
	cfg, cfgErr := sextantd.LoadConfig(sextantdTomlPath)
	if cfgErr != nil {
		out = append(out, CheckResult{
			Kind: "config", Check: sextantdTomlPath,
			Status: StatusFail, Detail: cfgErr.Error(),
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
		return CheckResult{Kind: "ca", Check: keyPath, Status: StatusFail, Detail: err.Error()}
	case st.Mode().Perm() != 0o600:
		return CheckResult{
			Kind: "ca", Check: keyPath, Status: StatusFail,
			Detail: fmt.Sprintf("mode %o, want 0600", st.Mode().Perm()),
		}
	}
	if _, err := authjwt.LoadCA(keyPath, pubPath); err != nil {
		return CheckResult{Kind: "ca", Check: keyPath, Status: StatusFail, Detail: err.Error()}
	}
	return CheckResult{Kind: "ca", Check: keyPath, Status: StatusPass, Detail: "ed25519 keypair valid"}
}

func checkOperatorCreds(path string) CheckResult {
	st, err := os.Stat(path)
	if err != nil {
		return CheckResult{Kind: "operator-creds", Check: path, Status: StatusFail, Detail: err.Error()}
	}
	if st.Mode().Perm() != 0o600 {
		return CheckResult{
			Kind: "operator-creds", Check: path, Status: StatusFail,
			Detail: fmt.Sprintf("mode %o, want 0600", st.Mode().Perm()),
		}
	}
	if _, err := sextantd.ReadOperatorCreds(path); err != nil {
		return CheckResult{Kind: "operator-creds", Check: path, Status: StatusFail, Detail: err.Error()}
	}
	return CheckResult{Kind: "operator-creds", Check: path, Status: StatusPass, Detail: "loaded"}
}

func checkClickHousePassword(path string) CheckResult {
	st, err := os.Stat(path)
	if err != nil {
		return CheckResult{Kind: "clickhouse-password", Check: path, Status: StatusFail, Detail: err.Error()}
	}
	if st.Mode().Perm() != 0o600 {
		return CheckResult{
			Kind: "clickhouse-password", Check: path, Status: StatusFail,
			Detail: fmt.Sprintf("mode %o, want 0600", st.Mode().Perm()),
		}
	}
	if _, err := sextantd.ReadPasswordFile(path); err != nil {
		return CheckResult{Kind: "clickhouse-password", Check: path, Status: StatusFail, Detail: err.Error()}
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

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// daemon_lifecycle.go owns the shared helpers behind `sextant start`,
// `sextant stop`, `sextant restart`, `sextant status`, and `sextant logs`.
// Each verb is a thin wrapper around these primitives so that the
// behaviour stays consistent (e.g. "is the daemon up" means the same
// thing in `start` as it does in `status`).
//
// Plan: slug:feat-daemon-lifecycle-ergonomics (fix #3).

// daemonLogFilename is the canonical filename for the daemon's combined
// stdout/stderr log. Sits inside the configured DataDir so an operator
// only needs to know the data-dir convention to find it. Other agents
// touching sextantd are standardising on the same path.
const daemonLogFilename = "sextantd.log"

// errDaemonNotRunning is returned by readDaemonState when runtime.json is
// absent. Callers distinguish "not running" from "I/O error" so they can
// keep `sextant stop`/`sextant status` idempotent.
var errDaemonNotRunning = errors.New("daemon not running")

// daemonState is what readDaemonState returns: the parsed runtime.json
// info plus a derived "is the recorded PID still alive" flag. We probe
// up-front so callers don't repeat the syscall.
type daemonState struct {
	Info sextantd.RuntimeInfo
	// Alive is true when Info.PID is a live process owned by us (or at
	// least signal-able). Always check this before trusting the rest of
	// the struct — a stale runtime.json is a real failure mode.
	Alive bool
}

// readDaemonState loads runtime.json from cfg.Paths.RuntimeFile and
// probes the recorded PID. Returns errDaemonNotRunning (wrapped) if the
// file is absent. Other read errors propagate as-is.
func readDaemonState(runtimeFile string) (daemonState, error) {
	info, err := sextantd.ReadRuntimeInfo(runtimeFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
			return daemonState{}, errDaemonNotRunning
		}
		return daemonState{}, err
	}
	return daemonState{Info: info, Alive: isProcessAlive(info.PID)}, nil
}

// isProcessAlive returns true when pid is signal-able with signal 0
// (the conventional liveness probe — does not deliver a signal). A
// false here means the process is gone, has been recycled, or belongs
// to another uid we can't reach. From the operator's perspective those
// are all "stale runtime.json".
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		// Unix: FindProcess never errors; Windows: not a target. Keep
		// the branch defensive anyway.
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// findSextantdBinary resolves the path to the sextantd binary using the
// search order described in the issue: $SEXTANTD_BIN first (an explicit
// operator override), then the directory next to the running sextant
// binary (so `make install` lays them side by side and no extra config
// is needed), then $PATH. Each candidate must exist and be executable.
func findSextantdBinary() (string, error) {
	// 1. Explicit override.
	if v := strings.TrimSpace(os.Getenv("SEXTANTD_BIN")); v != "" {
		if err := checkExecutable(v); err != nil {
			return "", fmt.Errorf("SEXTANTD_BIN=%q: %w", v, err)
		}
		return v, nil
	}
	// 2. Sibling of `os.Executable()`. Resolve symlinks so we follow
	//    `make install`'s `install -s` shim if any.
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		candidate := filepath.Join(filepath.Dir(exe), "sextantd")
		if err := checkExecutable(candidate); err == nil {
			return candidate, nil
		}
	}
	// 3. $PATH.
	if path, err := exec.LookPath("sextantd"); err == nil {
		return path, nil
	}
	return "", errors.New("sextantd binary not found (set SEXTANTD_BIN, " +
		"install next to sextant, or add to $PATH)")
}

func checkExecutable(path string) error {
	st, err := os.Stat(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if st.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable (mode %o)", path, st.Mode().Perm())
	}
	return nil
}

// daemonLogPath returns the canonical log path for the daemon. We prefer
// runtime.json's LogFile field when present (the daemon writes it on
// startup); otherwise fall back to <dataDir>/sextantd.log so the
// command still works if runtime.json is stale or pre-dates the field.
func daemonLogPath(dataDir string, info sextantd.RuntimeInfo) string {
	if info.LogFile != "" {
		return info.LogFile
	}
	return filepath.Join(dataDir, daemonLogFilename)
}

// waitForDaemonUp polls runtime.json until it appears AND the recorded
// PID is alive, or timeout elapses. Returns the final daemonState on
// success.
func waitForDaemonUp(runtimeFile string, timeout time.Duration) (daemonState, error) {
	const interval = 250 * time.Millisecond
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		st, err := readDaemonState(runtimeFile)
		switch {
		case err == nil && st.Alive:
			return st, nil
		case err != nil && !errors.Is(err, errDaemonNotRunning):
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return daemonState{}, fmt.Errorf("timed out after %s waiting for daemon (last err: %w)", timeout, lastErr)
			}
			return daemonState{}, fmt.Errorf("timed out after %s waiting for daemon to start", timeout)
		}
		time.Sleep(interval)
	}
}

// waitForDaemonGone polls until runtime.json disappears or timeout
// elapses. The daemon's SIGTERM handler removes the file on graceful
// shutdown (see cmd/sextantd/daemon.go), so the file's absence is the
// signal we use to know shutdown finished.
func waitForDaemonGone(runtimeFile string, timeout time.Duration) error {
	const interval = 250 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Stat(runtimeFile)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for runtime.json to disappear", timeout)
		}
		time.Sleep(interval)
	}
}

// tailLogLines reads the last n non-empty lines of path. Used by `start`
// to surface the tail when boot times out and by `logs --tail N`.
// Implementation is deliberately naive (read entire file) — the daemon
// log isn't expected to grow large between operator inspections and
// rotating-tail code adds complexity we don't need yet.
func tailLogLines(path string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) <= n {
		return lines, nil
	}
	return lines[len(lines)-n:], nil
}

// printLines writes each line in lines to w as a separate \n-terminated
// row. Centralised so the start-failure tail and `sextant logs --tail`
// produce identical output.
func printLines(w io.Writer, lines []string) {
	for _, line := range lines {
		printf(w, "%s\n", line)
	}
}

// findOrphanSextantd scans the process table for sextantd processes
// whose first argv token matches binaryPath, excluding excludePID (the
// daemon recorded in runtime.json, if any). Returns the matching PIDs
// sorted ascending. Used by `sextant start` to detect zombies — a
// sextantd whose runtime.json was removed but whose process kept
// running — and by `sextant stop` to clean them up.
//
// We match on the full path (not basename) so an unrelated binary that
// happens to be named "sextantd" elsewhere on the box can't trigger a
// false positive. binaryPath is whatever findSextantdBinary would
// resolve to in the current operator's setup.
//
// Implementation: shells out to `ps -axo pid=,command=`. Portable
// across macOS + Linux; the trailing `=` suppresses the column header
// so we get pure rows. Errors propagate as-is.
func findOrphanSextantd(binaryPath string, excludePID int) ([]int, error) {
	if binaryPath == "" {
		return nil, errors.New("findOrphanSextantd: binaryPath required")
	}
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output() //nolint:gosec // fixed args
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "<pid> <command...>". Split on the first run of whitespace so
		// the command (which may contain spaces in its args) stays intact.
		sp := strings.IndexAny(line, " \t")
		if sp < 0 {
			continue
		}
		pidStr := line[:sp]
		cmd := strings.TrimSpace(line[sp:])
		pid := 0
		for _, ch := range pidStr {
			if ch < '0' || ch > '9' {
				pid = 0
				break
			}
			pid = pid*10 + int(ch-'0')
		}
		if pid <= 0 || pid == excludePID {
			continue
		}
		// argv[0] is the leading token of the command line. Match exact
		// path so we don't catch unrelated binaries with the same name.
		// macOS/Linux both render argv[0] verbatim under -o command.
		firstTok := cmd
		if i := strings.IndexAny(cmd, " \t"); i >= 0 {
			firstTok = cmd[:i]
		}
		if firstTok == binaryPath {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// sigtermPIDs sends SIGTERM to each pid and waits up to timeout for
// every one to disappear. Returns the slice of PIDs still alive at the
// deadline. Best-effort: ESRCH on signal (process already gone) is
// treated as success for that pid.
func sigtermPIDs(pids []int, timeout time.Duration) []int {
	if len(pids) == 0 {
		return nil
	}
	for _, pid := range pids {
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if sigErr := proc.Signal(syscall.SIGTERM); sigErr != nil && !errors.Is(sigErr, syscall.ESRCH) {
			// We don't fail outright — the caller decides what to do
			// with a still-alive return list.
			continue
		}
	}
	const interval = 100 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var stillAlive []int
		for _, pid := range pids {
			if isProcessAlive(pid) {
				stillAlive = append(stillAlive, pid)
			}
		}
		if len(stillAlive) == 0 {
			return nil
		}
		pids = stillAlive
		time.Sleep(interval)
	}
	var remaining []int
	for _, pid := range pids {
		if isProcessAlive(pid) {
			remaining = append(remaining, pid)
		}
	}
	return remaining
}

// loadDaemonConfig resolves config-dir / data-dir flags through the
// shared init helper, loads sextantd.toml (or falls back to defaults
// when the config file is missing — `sextant start` should still work
// before `sextant init` writes the file because the underlying daemon
// has its own defaults too).
func loadDaemonConfig(configDirFlag, dataDirFlag string) (sextantd.Config, error) {
	cfgDir, dataDir, err := resolveInitPaths(configDirFlag, dataDirFlag)
	if err != nil {
		return sextantd.Config{}, err
	}
	path := filepath.Join(cfgDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(path)
	if err == nil {
		return cfg, nil
	}
	// Missing config falls back to defaults so `start` can at least try
	// the canonical locations. A real parse error still bubbles up.
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
		return sextantd.DefaultConfig(cfgDir, dataDir), nil
	}
	return sextantd.Config{}, err
}

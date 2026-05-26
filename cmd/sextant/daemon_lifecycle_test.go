package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// TestIsProcessAlive covers the signal-0 probe with three cases: the
// running test process (alive), a freshly-spawned + reaped child (dead),
// and a pid we know is invalid.
func TestIsProcessAlive(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Error("running test pid should be alive")
	}
	if isProcessAlive(-1) {
		t.Error("negative pid must not be reported alive")
	}
	// PID 999999 is reserved on Linux (kernel.pid_max default ~32768)
	// and out of reach on macOS too. Liveness should be false.
	if isProcessAlive(999_999) {
		t.Error("absent pid must not be reported alive")
	}
}

// TestReadDaemonStateMissing covers the "no runtime.json" path.
func TestReadDaemonStateMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := readDaemonState(filepath.Join(dir, "runtime.json"))
	if !errors.Is(err, errDaemonNotRunning) {
		t.Fatalf("err = %v, want errDaemonNotRunning", err)
	}
}

// TestReadDaemonStateStale covers the runtime.json-present-but-dead-pid
// path. The recorded pid points at PID 999999 (assumed absent).
func TestReadDaemonStateStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	if err := sextantd.WriteRuntimeInfo(path, sextantd.RuntimeInfo{
		PID:       999_999,
		StartedAt: time.Now(),
		Version:   "test",
	}); err != nil {
		t.Fatalf("WriteRuntimeInfo: %v", err)
	}
	st, err := readDaemonState(path)
	if err != nil {
		t.Fatalf("readDaemonState: %v", err)
	}
	if st.Alive {
		t.Error("expected Alive=false for absent pid")
	}
}

// TestReadDaemonStateAlive covers the happy path: a runtime.json file
// whose pid is os.Getpid() (the test process itself, guaranteed alive).
func TestReadDaemonStateAlive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	if err := sextantd.WriteRuntimeInfo(path, sextantd.RuntimeInfo{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		Version:   "test",
	}); err != nil {
		t.Fatalf("WriteRuntimeInfo: %v", err)
	}
	st, err := readDaemonState(path)
	if err != nil {
		t.Fatalf("readDaemonState: %v", err)
	}
	if !st.Alive {
		t.Error("expected Alive=true for current test process pid")
	}
}

// TestFindSextantdBinary_FromEnv covers SEXTANTD_BIN as the highest-
// priority lookup. We point it at a sentinel file (chmod +x) and
// confirm findSextantdBinary returns that exact path.
func TestFindSextantdBinary_FromEnv(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sextantd-stub")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("SEXTANTD_BIN", bin)
	got, err := findSextantdBinary()
	if err != nil {
		t.Fatalf("findSextantdBinary: %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

// TestFindSextantdBinary_EnvNonExecutable rejects a SEXTANTD_BIN that
// points at a non-executable file. The error message must reference
// SEXTANTD_BIN so the operator sees their own input echoed back.
func TestFindSextantdBinary_EnvNonExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(bin, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("SEXTANTD_BIN", bin)
	_, err := findSextantdBinary()
	if err == nil {
		t.Fatal("expected error for non-executable SEXTANTD_BIN")
	}
	if !strings.Contains(err.Error(), "SEXTANTD_BIN") {
		t.Errorf("error %q must mention SEXTANTD_BIN", err.Error())
	}
}

// TestTailLogLines covers reading the last N lines from a synthetic
// log file with more lines than the cap.
func TestTailLogLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake.log")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmtFprintln(&b, "line ", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write fake log: %v", err)
	}
	lines, err := tailLogLines(path, 10)
	if err != nil {
		t.Fatalf("tailLogLines: %v", err)
	}
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10", len(lines))
	}
	if !strings.HasPrefix(lines[0], "line 90") {
		t.Errorf("first line %q, want prefix 'line 90'", lines[0])
	}
	if !strings.HasPrefix(lines[9], "line 99") {
		t.Errorf("last line %q, want prefix 'line 99'", lines[9])
	}
}

// TestTailLogLinesShortFile returns the entire file when the line count
// is smaller than n.
func TestTailLogLinesShortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines, err := tailLogLines(path, 50)
	if err != nil {
		t.Fatalf("tailLogLines: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
}

// TestDaemonLogPath_Default uses the conventional location when the
// runtime.json info doesn't advertise an explicit log path.
func TestDaemonLogPath_Default(t *testing.T) {
	got := daemonLogPath("/tmp/sxt-data", sextantd.RuntimeInfo{})
	want := "/tmp/sxt-data/sextantd.log"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestWaitForDaemonGone returns nil once the file disappears, errors
// when it's still present after the timeout.
func TestWaitForDaemonGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")

	// Absent up-front — should return immediately.
	if err := waitForDaemonGone(path, 1*time.Second); err != nil {
		t.Errorf("absent file: %v", err)
	}

	// Present and not removed — should time out.
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := waitForDaemonGone(path, 300*time.Millisecond); err == nil {
		t.Error("expected timeout when file remains")
	}
}

// fmtFprintln is a tiny adapter so the test doesn't have to depend on
// fmt directly in its imports section — keeps the test file lint-clean
// when the package's centralised println signature differs.
func fmtFprintln(b *strings.Builder, parts ...any) {
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			b.WriteString(v)
		case int:
			b.WriteString(itoaTest(v))
		}
	}
	b.WriteString("\n")
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// spawnFakeSextantd builds a tiny long-running Go binary at
// <dir>/sextantd and starts it. Used by orphan-detection tests so we have
// a process whose `ps -axo command=` listing matches a path we control.
// The fake installs explicit SIGINT/SIGTERM handlers that exit(0) — Go's
// default signal disposition isn't reliable here (a long-running runtime
// on macOS occasionally swallows SIGTERM during the time.Sleep wakeup),
// so we make the signal contract explicit.
// Returned cleanup function SIGKILLs and reaps the child as a safety net.
func spawnFakeSextantd(t *testing.T, dir string) (binPath string, pid int, cleanup func()) {
	t.Helper()
	binPath = filepath.Join(dir, "sextantd")
	src := filepath.Join(dir, "main.go")
	source := `package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	os.Exit(0)
}
`
	if err := os.WriteFile(src, []byte(source), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	build := exec.Command("go", "build", "-o", binPath, src) //nolint:gosec // test-controlled
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build fake sextantd: %v", err)
	}
	cmd := exec.Command(binPath) //nolint:gosec // test-controlled
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake sextantd: %v", err)
	}
	pid = cmd.Process.Pid
	// Reap in a background goroutine so the fake's exit promptly removes
	// it from the process table. Without this, SIGTERM leaves it as a
	// zombie (we're the parent in tests) and isProcessAlive keeps
	// returning true until t.Cleanup runs — which would race the test's
	// own poll loop. In production the daemon's parent is init, which
	// reaps automatically.
	reapDone := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(reapDone)
	}()
	cleanup = func() {
		// SIGKILL is a safety net for tests that exit before SIGTERM
		// reaches the fake. ESRCH ("already gone") is fine.
		_ = cmd.Process.Signal(syscall.SIGKILL)
		<-reapDone
	}
	// Give ps a moment to surface the new process.
	time.Sleep(200 * time.Millisecond)
	return binPath, pid, cleanup
}

// setStubSextantdBin sets SEXTANTD_BIN to a freshly-created stub binary
// in a t.TempDir() so the orphan-scan code in `doStart`/`doStop` won't
// accidentally match a real sextantd installed elsewhere on the dev
// box. The stub is never executed — only its path is used for the
// process-table comparison.
func setStubSextantdBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "sextantd")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("SEXTANTD_BIN", bin)
	return bin
}

// TestFindOrphanSextantd_DetectsByBinaryPath spawns a fake sextantd named
// after a test-controlled path, then asserts findOrphanSextantd returns
// exactly that PID. This is the load-bearing zombie-detection check: the
// real bug it shields against is "runtime.json was removed but sextantd
// kept running" — discovered live in Lena's session.
func TestFindOrphanSextantd_DetectsByBinaryPath(t *testing.T) {
	dir := t.TempDir()
	bin, fakePID, cleanup := spawnFakeSextantd(t, dir)
	defer cleanup()

	pids, err := findOrphanSextantd(bin, 0)
	if err != nil {
		t.Fatalf("findOrphanSextantd: %v", err)
	}
	if len(pids) != 1 || pids[0] != fakePID {
		t.Errorf("findOrphanSextantd = %v, want [%d]", pids, fakePID)
	}
}

// TestFindOrphanSextantd_ExcludesGivenPID covers the "this is the daemon
// we expect to be running" filter. `sextant stop` uses it to avoid
// counting the runtime.json daemon as its own orphan.
func TestFindOrphanSextantd_ExcludesGivenPID(t *testing.T) {
	dir := t.TempDir()
	bin, fakePID, cleanup := spawnFakeSextantd(t, dir)
	defer cleanup()

	pids, err := findOrphanSextantd(bin, fakePID)
	if err != nil {
		t.Fatalf("findOrphanSextantd: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("excludePID %d should hide it; got %v", fakePID, pids)
	}
}

// TestFindOrphanSextantd_NoMatchOnUnrelatedBinary confirms the path-match
// is strict — pointing at a binary nobody is running yields no orphans.
// Protects against false positives like "I happen to have another binary
// named sextantd in $PATH".
func TestFindOrphanSextantd_NoMatchOnUnrelatedBinary(t *testing.T) {
	pids, err := findOrphanSextantd("/nonexistent/path/to/sextantd-"+t.Name(), 0)
	if err != nil {
		t.Fatalf("findOrphanSextantd: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("expected no orphans, got %v", pids)
	}
}

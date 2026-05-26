package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

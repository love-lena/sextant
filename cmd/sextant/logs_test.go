package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogs_TailN — write a fake log of 100 lines, call doLogs with
// tail=10, assert exactly the last 10 lines come out (no follow).
func TestLogs_TailN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sextantd.log")
	var b bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatalf("write fake log: %v", err)
	}

	var out bytes.Buffer
	if err := doLogs(context.Background(), &out, path, 10, false); err != nil {
		t.Fatalf("doLogs: %v", err)
	}
	got := strings.TrimRight(out.String(), "\n")
	lines := strings.Split(got, "\n")
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10:\n%s", len(lines), got)
	}
	if lines[0] != "line 90" {
		t.Errorf("first line = %q, want 'line 90'", lines[0])
	}
	if lines[9] != "line 99" {
		t.Errorf("last line = %q, want 'line 99'", lines[9])
	}
}

// TestLogs_Missing returns a clear error and never blocks. Important
// because the obvious mistake is to run `sextant logs` before
// `sextant start` has ever produced a log file.
func TestLogs_Missing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.log")
	var out bytes.Buffer
	err := doLogs(context.Background(), &out, path, 10, false)
	if err == nil {
		t.Fatal("expected error for missing log file")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want 'does not exist' substring", err.Error())
	}
}

// TestLogs_Follow streams new bytes appended after doLogs starts. We
// cancel the context to make the follow loop return cleanly. Without
// this test the polling/exit semantics are easy to get wrong (e.g. an
// infinite loop that ignores ctx).
func TestLogs_Follow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.log")
	// Seed with one line so the tail-then-follow path has something to
	// print before we append.
	if err := os.WriteFile(path, []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- doLogs(ctx, &out, path, 1, true)
	}()

	// Give follow loop a moment to seek to end and start polling.
	time.Sleep(300 * time.Millisecond)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := f.WriteString("appended\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()

	// Wait for the polling loop to surface the append, then cancel.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "appended") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("follow returned err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not return after cancel")
	}
	if !strings.Contains(out.String(), "appended") {
		t.Errorf("follow did not surface appended line:\n%s", out.String())
	}
}

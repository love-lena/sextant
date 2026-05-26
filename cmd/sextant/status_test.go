package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// TestStatus_NotRunning_Exit1 covers the no-daemon case: doStatus
// returns errStatusNotRunning (which the dispatcher maps to exit 1)
// and the human-readable line is printed.
func TestStatus_NotRunning_Exit1(t *testing.T) {
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	var buf bytes.Buffer
	err := doStatus(&buf, cfg, false)
	if !errors.Is(err, errStatusNotRunning) {
		t.Fatalf("err = %v, want errStatusNotRunning", err)
	}
	if !strings.Contains(buf.String(), "daemon: not running") {
		t.Errorf("missing not-running line: %q", buf.String())
	}
}

// TestStatus_StaleRuntimeFile_Exit1 covers the stale path. doStatus
// must NOT remove the file (that's `start`/`stop`'s job) — it only
// reports the state. Otherwise consecutive `status` calls would race
// the cleanup.
func TestStatus_StaleRuntimeFile_Exit1(t *testing.T) {
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	stale := sextantd.RuntimeInfo{PID: 999_999, StartedAt: time.Now(), Version: "stale"}
	if err := sextantd.WriteRuntimeInfo(cfg.Paths.RuntimeFile, stale); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	err := doStatus(&buf, cfg, false)
	if !errors.Is(err, errStatusNotRunning) {
		t.Fatalf("err = %v, want errStatusNotRunning", err)
	}
	out := buf.String()
	if !strings.Contains(out, "stale") {
		t.Errorf("missing 'stale' marker: %q", out)
	}
	if _, err := os.Stat(cfg.Paths.RuntimeFile); err != nil {
		t.Errorf("status must not remove runtime.json; got err=%v", err)
	}
}

// TestStatus_Running_PrintsTable covers the happy path. We synthesise
// a runtime.json that points at os.Getpid() so doStatus's signal-0
// probe reports alive and the formatter exercises every row.
func TestStatus_Running_PrintsTable(t *testing.T) {
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	rt := sextantd.RuntimeInfo{
		PID:            os.Getpid(),
		StartedAt:      time.Now().Add(-30 * time.Second),
		NATSAddr:       "127.0.0.1:4222",
		NATSPID:        12345,
		ClickHouseTCP:  "127.0.0.1:9000",
		ClickHousePID:  12346,
		MCPHTTPAddr:    "127.0.0.1:5172",
		MCPStdioSocket: filepath.Join(opts.DataDir, "mcp.sock"),
		Version:        "test",
	}
	if err := sextantd.WriteRuntimeInfo(cfg.Paths.RuntimeFile, rt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	if err := doStatus(&buf, cfg, false); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"daemon", "nats", "clickhouse", "mcp", "log", "pid", "uptime", "127.0.0.1:4222"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in status table: %s", want, out)
		}
	}
}

// TestStatus_RunningJSON_ParsesAndMatches asserts the --json contract
// is structured + deterministic. A scriptable consumer must be able to
// parse the output without reaching for awk/sed.
func TestStatus_RunningJSON_ParsesAndMatches(t *testing.T) {
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	rt := sextantd.RuntimeInfo{
		PID:           os.Getpid(),
		StartedAt:     time.Now(),
		NATSAddr:      "127.0.0.1:4222",
		NATSPID:       12345,
		ClickHouseTCP: "127.0.0.1:9000",
		ClickHousePID: 12346,
		Version:       "test",
	}
	if err := sextantd.WriteRuntimeInfo(cfg.Paths.RuntimeFile, rt); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	if err := doStatus(&buf, cfg, true); err != nil {
		t.Fatalf("doStatus json: %v", err)
	}
	var row statusRow
	if err := json.Unmarshal(buf.Bytes(), &row); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, buf.String())
	}
	if row.State != "running" {
		t.Errorf("state = %q, want 'running'", row.State)
	}
	if row.Daemon.PID != os.Getpid() {
		t.Errorf("daemon.pid = %d, want %d", row.Daemon.PID, os.Getpid())
	}
	if row.NATS.PID != 12345 {
		t.Errorf("nats.pid = %d, want 12345", row.NATS.PID)
	}
}

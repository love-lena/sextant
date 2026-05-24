package sextantd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// RuntimeInfo records the live ports and PID of a running daemon. The
// file is written by sextantd on startup and read by `sextant doctor`
// (and any other tool that needs to find the live daemon's listeners).
// Minimal schema in M5; promoted to a typed-package consumer as more
// readers appear.
//
// NATSPID and ClickHousePID are the subprocess PIDs as observed at the
// most recent (re)start. Used by tests to drive restart-on-failure
// behavior and by operators who want to inspect a specific subprocess.
type RuntimeInfo struct {
	PID            int       `json:"pid"`
	StartedAt      time.Time `json:"started_at"`
	NATSAddr       string    `json:"nats_addr"`
	NATSPID        int       `json:"nats_pid,omitempty"`
	ClickHouseTCP  string    `json:"clickhouse_tcp"`
	ClickHouseHTTP string    `json:"clickhouse_http"`
	ClickHousePID  int       `json:"clickhouse_pid,omitempty"`
	ControlSocket  string    `json:"control_socket"`
	Version        string    `json:"version"`
}

// WriteRuntimeInfo persists info to path with mode 0600. The parent dir
// must exist.
func WriteRuntimeInfo(path string, info RuntimeInfo) error {
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("sextantd: marshal runtime info: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("sextantd: write %s: %w", path, err)
	}
	return nil
}

// ReadRuntimeInfo parses path. Returns os.ErrNotExist (wrapped) if the
// file is absent so callers can distinguish "daemon not running" from
// "filesystem error".
func ReadRuntimeInfo(path string) (RuntimeInfo, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled
	if err != nil {
		return RuntimeInfo{}, fmt.Errorf("sextantd: read %s: %w", path, err)
	}
	var info RuntimeInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return RuntimeInfo{}, fmt.Errorf("sextantd: parse %s: %w", path, err)
	}
	return info, nil
}

// RemoveRuntimeInfo deletes path. Used during daemon shutdown.
// os.ErrNotExist is treated as a successful no-op.
func RemoveRuntimeInfo(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sextantd: remove %s: %w", path, err)
	}
	return nil
}

package sextantd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfigPaths(t *testing.T) {
	cfg := DefaultConfig("/cfg", "/data")
	if cfg.Daemon.ControlSocket != "/data/sextantd.sock" {
		t.Errorf("control_socket = %s", cfg.Daemon.ControlSocket)
	}
	if cfg.CA.KeyPath != "/cfg/ca.key" {
		t.Errorf("ca.key_path = %s", cfg.CA.KeyPath)
	}
	if cfg.NATS.OperatorCreds != "/cfg/operator.creds" {
		t.Errorf("operator_creds = %s", cfg.NATS.OperatorCreds)
	}
	if cfg.Daemon.ShutdownTimeout.AsDuration() != 30*time.Second {
		t.Errorf("shutdown_timeout = %s", cfg.Daemon.ShutdownTimeout.AsDuration())
	}
	if cfg.Daemon.RestartQuarantineAfter != 5 {
		t.Errorf("restart_quarantine_after = %d", cfg.Daemon.RestartQuarantineAfter)
	}
}

func TestSaveAndLoadConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "cfg")
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	in := DefaultConfig(cfgDir, dataDir)
	path := filepath.Join(cfgDir, "sextantd.toml")
	if err := SaveConfig(path, in); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	// Mode is 0600.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", st.Mode().Perm())
	}
	out, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if out.Daemon.ControlSocket != in.Daemon.ControlSocket {
		t.Errorf("control_socket %s != %s", out.Daemon.ControlSocket, in.Daemon.ControlSocket)
	}
	if out.NATS.DataDir != in.NATS.DataDir {
		t.Errorf("nats.data_dir mismatch")
	}
}

func TestResolveExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	cfg := DefaultConfig("~/.config/sextant", "~/.local/share/sextant")
	out, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(home, ".config", "sextant", "ca.key")
	if out.CA.KeyPath != want {
		t.Errorf("ca.key_path = %s, want %s", out.CA.KeyPath, want)
	}
}

func TestResolveRequiresFields(t *testing.T) {
	var empty Config
	if _, err := empty.Resolve(); err == nil {
		t.Fatal("expected Resolve to reject empty config")
	}
}

func TestOperatorCredsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "operator.creds")
	pw, err := GenerateOperatorPassword()
	if err != nil {
		t.Fatalf("GenerateOperatorPassword: %v", err)
	}
	if len(pw) < 32 {
		t.Fatalf("password too short: %d", len(pw))
	}
	if err := WriteOperatorCreds(path, OperatorCreds{User: "operator", Password: pw}); err != nil {
		t.Fatalf("WriteOperatorCreds: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", st.Mode().Perm())
	}
	got, err := ReadOperatorCreds(path)
	if err != nil {
		t.Fatalf("ReadOperatorCreds: %v", err)
	}
	if got.User != "operator" || got.Password != pw {
		t.Errorf("creds mismatch: %+v", got)
	}
}

func TestRuntimeInfoRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	in := RuntimeInfo{
		PID:           4242,
		StartedAt:     time.Now().UTC().Truncate(time.Second),
		NATSAddr:      "127.0.0.1:4222",
		ClickHouseTCP: "127.0.0.1:9000",
		ControlSocket: "/tmp/sextantd.sock",
		Version:       "test",
	}
	if err := WriteRuntimeInfo(path, in); err != nil {
		t.Fatalf("WriteRuntimeInfo: %v", err)
	}
	out, err := ReadRuntimeInfo(path)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	if out.PID != in.PID || out.NATSAddr != in.NATSAddr {
		t.Errorf("roundtrip mismatch: %+v vs %+v", out, in)
	}
}

package clickhouseboot

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"time"
)

// Config governs a clickhouseboot.Server. Zero value is invalid; build
// with DefaultConfig.
type Config struct {
	// DataDir holds ClickHouse's data files. Created if missing.
	DataDir string

	// ListenHost is the bind host. Defaults "127.0.0.1"; sextant never
	// binds to a routable interface in initial.
	ListenHost string

	// HTTPPort is the HTTP listener port. 0 = OS-picked free port.
	HTTPPort int

	// TCPPort is the native protocol port. 0 = OS-picked free port.
	TCPPort int

	// Database is the default database name. Defaults to "sextant".
	Database string

	// User is the SQL user. Defaults to "sextant".
	User string

	// Password is the SQL user password. Empty triggers a 32-byte
	// random password.
	Password string

	// ClickHouseBinary lets callers override the executable path. Empty
	// resolves "clickhouse" on $PATH.
	ClickHouseBinary string

	// StartupTimeout caps the wait for a successful SELECT 1. Defaults
	// to 30 seconds because ClickHouse takes a few seconds to come up.
	StartupTimeout time.Duration

	// ShutdownTimeout caps the wait for graceful SIGTERM exit.
	ShutdownTimeout time.Duration

	// LogFile redirects stdout/stderr if non-empty.
	LogFile string
}

// DefaultConfig returns a Config rooted at dataDir with sextant defaults.
func DefaultConfig(dataDir string) Config {
	return Config{
		DataDir:         dataDir,
		ListenHost:      "127.0.0.1",
		HTTPPort:        0,
		TCPPort:         0,
		Database:        "sextant",
		User:            "sextant",
		StartupTimeout:  30 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

// validateAndFill returns a fully-defaulted Config or an error.
func (c Config) validateAndFill() (Config, error) {
	out := c
	if out.DataDir == "" {
		return Config{}, fmt.Errorf("clickhouseboot: DataDir is required")
	}
	if out.ListenHost == "" {
		out.ListenHost = "127.0.0.1"
	}
	if out.ListenHost == "0.0.0.0" {
		return Config{}, fmt.Errorf("clickhouseboot: refusing to bind on 0.0.0.0; use 127.0.0.1")
	}
	if out.Database == "" {
		out.Database = "sextant"
	}
	if out.User == "" {
		out.User = "sextant"
	}
	if out.Password == "" {
		p, err := randomPassword()
		if err != nil {
			return Config{}, fmt.Errorf("clickhouseboot: generate password: %w", err)
		}
		out.Password = p
	}
	if out.ClickHouseBinary == "" {
		out.ClickHouseBinary = "clickhouse"
	}
	if out.StartupTimeout <= 0 {
		out.StartupTimeout = 30 * time.Second
	}
	if out.ShutdownTimeout <= 0 {
		out.ShutdownTimeout = 10 * time.Second
	}
	if out.HTTPPort == 0 {
		p, err := pickFreePort(out.ListenHost)
		if err != nil {
			return Config{}, fmt.Errorf("clickhouseboot: pick http port: %w", err)
		}
		out.HTTPPort = p
	}
	if out.TCPPort == 0 {
		p, err := pickFreePort(out.ListenHost)
		if err != nil {
			return Config{}, fmt.Errorf("clickhouseboot: pick tcp port: %w", err)
		}
		out.TCPPort = p
	}
	if out.TCPPort == out.HTTPPort {
		return Config{}, fmt.Errorf("clickhouseboot: TCP and HTTP ports collide on %d", out.TCPPort)
	}
	return out, nil
}

func pickFreePort(host string) (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, fmt.Errorf("listen on %s:0: %w", host, err)
	}
	defer l.Close() //nolint:errcheck // port-probe close best-effort
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type %T", l.Addr())
	}
	return addr.Port, nil
}

func randomPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

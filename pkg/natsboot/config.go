package natsboot

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"time"
)

// Config governs a natsboot.Server. Zero value is invalid; build with
// DefaultConfig or supply every field that does not have a documented
// "auto-derived" rule.
type Config struct {
	// DataDir is where JetStream stores its files. The directory is
	// created with mode 0o750 if missing. Required.
	DataDir string

	// ServerName is the NATS server_name shown in logs and required
	// for JetStream clustering. Defaults to "sextant" when empty.
	ServerName string

	// ListenHost is the bind host. Defaults to "127.0.0.1". Sextant
	// never binds to 0.0.0.0 in initial.
	ListenHost string

	// ListenPort is the TCP port. Use 0 to ask natsboot to pick a free
	// port at start time (recommended for tests).
	ListenPort int

	// OperatorUser is the NATS auth username for the operator. Defaults
	// to "operator". Stored in operator.creds (M5 write); used by every
	// CLI/TUI/sextantd-internal NATS connection.
	OperatorUser string

	// OperatorPassword is the password for OperatorUser. If empty,
	// Build generates a 32-byte URL-safe random password.
	OperatorPassword string

	// JWTCAPubPath is the path to the sextant CA public key used to
	// verify per-agent JWTs. May be empty or point at a missing file
	// in M2 — no JWT-presenting clients connect until M11.
	JWTCAPubPath string

	// NATSBinary lets callers override the nats-server executable path
	// (used by tests to point at a specific binary). Empty means
	// `nats-server` resolved on $PATH.
	NATSBinary string

	// StartupTimeout caps how long we wait for nats-server to become
	// connect-able. Defaults to 10 seconds.
	StartupTimeout time.Duration

	// ShutdownTimeout caps how long we wait for nats-server to exit
	// gracefully after SIGINT. Defaults to 5 seconds.
	ShutdownTimeout time.Duration

	// LogFile, when non-empty, makes natsboot redirect nats-server's
	// stdout/stderr to this file (created with mode 0o640). Empty means
	// discard.
	LogFile string

	// MaxBytesPerStream sets the JetStream MaxBytes for every sextant
	// stream. Defaults to 1 GiB. Tune per environment.
	MaxBytesPerStream int64
}

// DefaultConfig returns a Config with sextant's default knobs filled in
// and DataDir set to the supplied path.
func DefaultConfig(dataDir string) Config {
	return Config{
		DataDir:           dataDir,
		ServerName:        "sextant",
		ListenHost:        "127.0.0.1",
		ListenPort:        0,
		OperatorUser:      "operator",
		StartupTimeout:    10 * time.Second,
		ShutdownTimeout:   5 * time.Second,
		MaxBytesPerStream: 1 << 30, // 1 GiB
	}
}

// validateAndFill checks invariants and fills defaults for unset fields.
// Returns the normalized config; mutates nothing in the receiver.
func (c Config) validateAndFill() (Config, error) {
	out := c
	if out.DataDir == "" {
		return Config{}, fmt.Errorf("natsboot: DataDir is required")
	}
	if out.ServerName == "" {
		out.ServerName = "sextant"
	}
	if out.ListenHost == "" {
		out.ListenHost = "127.0.0.1"
	}
	if out.ListenHost == "0.0.0.0" {
		return Config{}, fmt.Errorf("natsboot: refusing to bind NATS on 0.0.0.0; use 127.0.0.1 for initial")
	}
	if out.OperatorUser == "" {
		out.OperatorUser = "operator"
	}
	if out.OperatorPassword == "" {
		pass, err := randomPassword()
		if err != nil {
			return Config{}, fmt.Errorf("natsboot: generate operator password: %w", err)
		}
		out.OperatorPassword = pass
	}
	if out.StartupTimeout <= 0 {
		out.StartupTimeout = 10 * time.Second
	}
	if out.ShutdownTimeout <= 0 {
		out.ShutdownTimeout = 5 * time.Second
	}
	if out.NATSBinary == "" {
		out.NATSBinary = "nats-server"
	}
	if out.MaxBytesPerStream <= 0 {
		out.MaxBytesPerStream = 1 << 30
	}
	if out.ListenPort == 0 {
		port, err := pickFreePort(out.ListenHost)
		if err != nil {
			return Config{}, fmt.Errorf("natsboot: pick free port on %s: %w", out.ListenHost, err)
		}
		out.ListenPort = port
	}
	return out, nil
}

// pickFreePort asks the OS for an unused TCP port on host. The returned
// port is racy by definition; callers should consume it promptly.
func pickFreePort(host string) (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, fmt.Errorf("listen on %s:0: %w", host, err)
	}
	defer l.Close() //nolint:errcheck // port-probe close is best-effort
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type %T", l.Addr())
	}
	return addr.Port, nil
}

// randomPassword returns 32 base64url bytes of crypto-random data with
// no padding. Sufficient entropy for NATS auth.
func randomPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

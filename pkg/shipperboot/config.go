package shipperboot

import (
	"fmt"
	"time"
)

// Config governs a shipperboot.Server. Zero value is invalid; build with
// DefaultConfig and set the BinaryPath + ConfigPath the daemon resolved.
type Config struct {
	// BinaryPath is the absolute path to the sextant-shipper executable.
	// Required. The daemon resolves it by looking in the same directory
	// as the sextantd binary (os.Executable + filepath.Dir) and falls
	// back to a PATH lookup; shipperboot itself does no resolution.
	BinaryPath string

	// ConfigPath is the absolute path to shipper.toml passed via
	// --config. Required: the shipper refuses to start without one.
	ConfigPath string

	// RuntimePath is the absolute path to runtime.json passed via
	// --runtime-file. Required when shipper.toml leaves NATS / ClickHouse
	// addresses empty (the normal case under sextantd auto-supervision).
	RuntimePath string

	// LogFile redirects stdout/stderr if non-empty. Empty routes to
	// /dev/null — mirrors clickhouseboot and natsboot.
	LogFile string

	// StartupGrace is how long we wait after Start before we trust the
	// subprocess to stay up. The shipper has no readiness endpoint; we
	// instead watch cmd.Wait() for an early exit during this window.
	// Defaults to 2 seconds.
	StartupGrace time.Duration

	// ShutdownTimeout caps the SIGTERM → SIGKILL escalation window.
	// Defaults to 10 seconds.
	ShutdownTimeout time.Duration
}

// DefaultConfig returns a Config with sextant defaults. Caller must
// still set BinaryPath, ConfigPath, and RuntimePath.
func DefaultConfig() Config {
	return Config{
		StartupGrace:    2 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

func (c Config) validateAndFill() (Config, error) {
	out := c
	if out.BinaryPath == "" {
		return Config{}, fmt.Errorf("shipperboot: BinaryPath is required")
	}
	if out.ConfigPath == "" {
		return Config{}, fmt.Errorf("shipperboot: ConfigPath is required")
	}
	if out.RuntimePath == "" {
		return Config{}, fmt.Errorf("shipperboot: RuntimePath is required")
	}
	if out.StartupGrace <= 0 {
		out.StartupGrace = 2 * time.Second
	}
	if out.ShutdownTimeout <= 0 {
		out.ShutdownTimeout = 10 * time.Second
	}
	return out, nil
}

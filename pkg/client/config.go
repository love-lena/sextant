package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the parsed shape of ~/.config/sextant/client.toml. See
// specs/components/client-libraries.md §"Config file" for the schema.
//
// Zero value is invalid; obtain a Config from LoadConfig or build one
// explicitly. Both code paths flow through validateAndFill so defaults
// are consistent.
type Config struct {
	NATS     NATSConfig     `toml:"nats"`
	Operator OperatorConfig `toml:"operator"`
	Client   ClientConfig   `toml:"client"`
}

// NATSConfig holds NATS-server connection details. Loopback only for
// initial; the spec refuses routable URLs.
type NATSConfig struct {
	// URL is the NATS server URL, e.g. "nats://127.0.0.1:4222".
	URL string `toml:"url"`
}

// OperatorConfig carries the operator-user credentials. Exactly one of
// Password / CredsPath must be set.
type OperatorConfig struct {
	// User is the NATS auth username; defaults to "operator".
	User string `toml:"user"`
	// Password is the inline operator password (optional in test/dev).
	Password string `toml:"password"`
	// CredsPath is the path to a NATS creds file written by `sextant
	// init` (M5+). When set, the library reads the file at connect time.
	CredsPath string `toml:"creds_path"`
}

// ClientConfig contains optional knobs the spec defaults if absent.
type ClientConfig struct {
	// ConnectTimeout caps the initial dial. Default 10s.
	ConnectTimeout Duration `toml:"connect_timeout"`
	// RequestTimeout is the default per-RPC timeout. Default 30s.
	RequestTimeout Duration `toml:"request_timeout"`
	// LogLevel is one of trace|debug|info|warn|error. Default "info".
	// M4 does not emit logs itself; the field reserves the slot for M5+.
	LogLevel string `toml:"log_level"`
}

// Duration is a time.Duration that TOML-unmarshals from a Go-style
// duration string ("10s", "500ms"). Necessary because pelletier/go-toml
// only decodes duration if the target field is time.Duration via its
// internal handler, which is what we get here transparently — keep this
// wrapper for symmetry with future YAML/JSON paths and to make the zero
// value detectable for defaulting.
type Duration time.Duration

// UnmarshalText parses a duration string per time.ParseDuration.
func (d *Duration) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	if s == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("client: parse duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalText emits the duration in time.Duration's String form.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// AsDuration converts to a plain time.Duration.
func (d Duration) AsDuration() time.Duration { return time.Duration(d) }

// DefaultConfigPath returns the canonical client.toml location:
// ~/.config/sextant/client.toml.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("client: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "sextant", "client.toml"), nil
}

// DefaultRuntimePath returns the canonical runtime.json location written
// by sextantd: ~/.local/share/sextant/runtime.json. This mirrors the
// default in pkg/sextantd's DefaultPaths/DefaultConfig.
//
// Connect consults this file (if it exists and parses) for the live NATS
// address, overriding the client.toml URL — which is a static placeholder
// from `sextant init` and goes stale whenever the daemon binds an
// auto-allocated port.
func DefaultRuntimePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("client: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "sextant", "runtime.json"), nil
}

// runtimeInfo is the minimal subset of sextantd's runtime.json the
// client needs. We mirror the field name (`nats_addr`) rather than
// importing pkg/sextantd so this low-level library stays independent.
// The shape matches pkg/sextantd.RuntimeInfo and pkg/shipper.RuntimeAddrs.
type runtimeInfo struct {
	NATSAddr string `json:"nats_addr"`
}

// readRuntimeNATSURL returns the live NATS URL recorded in runtime.json
// at path, or ok=false when the file is absent / unreadable / unparsable
// / has no nats_addr. Errors are intentionally swallowed: the caller
// falls back to the static client.toml URL, which is a strict
// improvement over the pre-fix behavior (always trusting the stale port).
func readRuntimeNATSURL(path string) (url string, ok bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled path
	if err != nil {
		return "", false
	}
	var info runtimeInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", false
	}
	if info.NATSAddr == "" {
		return "", false
	}
	return "nats://" + info.NATSAddr, true
}

// LoadConfig reads a TOML file from path, parses it, and fills defaults.
// `~/` in the input path is expanded against os.UserHomeDir.
func LoadConfig(path string) (Config, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(expanded) //nolint:gosec // operator-config controlled
	if err != nil {
		return Config{}, fmt.Errorf("client: read config %s: %w", expanded, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("client: parse config %s: %w", expanded, err)
	}
	out, err := cfg.validateAndFill()
	if err != nil {
		return Config{}, err
	}
	return out, nil
}

// validateAndFill returns a normalized Config or an error. It does not
// mutate the receiver.
func (c Config) validateAndFill() (Config, error) {
	out := c
	if out.NATS.URL == "" {
		return Config{}, fmt.Errorf("client: nats.url is required")
	}
	if out.Operator.User == "" {
		out.Operator.User = "operator"
	}
	if out.Operator.Password == "" && out.Operator.CredsPath == "" {
		return Config{}, fmt.Errorf("client: exactly one of operator.password or operator.creds_path must be set")
	}
	if out.Operator.Password != "" && out.Operator.CredsPath != "" {
		return Config{}, fmt.Errorf("client: operator.password and operator.creds_path are mutually exclusive")
	}
	if out.Operator.CredsPath != "" {
		expanded, err := expandHome(out.Operator.CredsPath)
		if err != nil {
			return Config{}, err
		}
		out.Operator.CredsPath = expanded
	}
	if out.Client.ConnectTimeout <= 0 {
		out.Client.ConnectTimeout = Duration(10 * time.Second)
	}
	if out.Client.RequestTimeout <= 0 {
		out.Client.RequestTimeout = Duration(30 * time.Second)
	}
	if out.Client.LogLevel == "" {
		out.Client.LogLevel = "info"
	}
	switch out.Client.LogLevel {
	case "trace", "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("client: invalid client.log_level %q (want trace|debug|info|warn|error)", out.Client.LogLevel)
	}
	return out, nil
}

// expandHome resolves a leading "~/" against os.UserHomeDir.
func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") && path != "~" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("client: resolve home dir: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

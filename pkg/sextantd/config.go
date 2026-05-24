package sextantd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the parsed shape of ~/.config/sextant/sextantd.toml. The
// schema is normative — see specs/components/sextantd.md §"sextantd.toml
// schema". Construct via DefaultConfig or LoadConfig; both flow through
// Resolve so every path is absolute on the result.
type Config struct {
	Daemon     DaemonConfig     `toml:"daemon"`
	CA         CAConfig         `toml:"ca"`
	NATS       NATSConfig       `toml:"nats"`
	ClickHouse ClickHouseConfig `toml:"clickhouse"`
	MCP        MCPConfig        `toml:"mcp"`
	Paths      PathsConfig      `toml:"paths"`
}

// DaemonConfig governs the daemon process itself.
type DaemonConfig struct {
	ControlSocket          string   `toml:"control_socket"`
	ShutdownTimeout        Duration `toml:"shutdown_timeout"`
	RestartBackoffInitial  Duration `toml:"restart_backoff_initial"`
	RestartBackoffMax      Duration `toml:"restart_backoff_max"`
	RestartQuarantineAfter int      `toml:"restart_quarantine_after"`
}

// CAConfig points at the signing-CA files.
type CAConfig struct {
	KeyPath string `toml:"key_path"`
	PubPath string `toml:"pub_path"`
}

// NATSConfig governs the supervised nats-server.
type NATSConfig struct {
	DataDir       string `toml:"data_dir"`
	ListenHost    string `toml:"listen_host"`
	ListenPort    int    `toml:"listen_port"`
	OperatorCreds string `toml:"operator_creds"`
	LogFile       string `toml:"log_file"`
}

// ClickHouseConfig governs the supervised clickhouse-server.
type ClickHouseConfig struct {
	DataDir      string `toml:"data_dir"`
	ListenHost   string `toml:"listen_host"`
	HTTPPort     int    `toml:"http_port"`
	TCPPort      int    `toml:"tcp_port"`
	Database     string `toml:"database"`
	User         string `toml:"user"`
	PasswordFile string `toml:"password_file"`
	LogFile      string `toml:"log_file"`
}

// MCPConfig governs the in-process MCP server (M10). HTTPHost+HTTPPort
// bind the agent-facing Streamable HTTP listener; StdioSocket is the
// operator-facing Unix socket. Both surfaces are spec'd in
// specs/components/sextantd.md §"MCP server".
type MCPConfig struct {
	HTTPHost    string `toml:"http_host"`
	HTTPPort    int    `toml:"http_port"`
	StdioSocket string `toml:"stdio_socket"`
}

// PathsConfig holds the rest of the path layout.
type PathsConfig struct {
	TemplatesDir string `toml:"templates_dir"`
	ClientConfig string `toml:"client_config"`
	RuntimeFile  string `toml:"runtime_file"`
	DataDir      string `toml:"data_dir"`
	ConfigDir    string `toml:"config_dir"`
}

// Duration wraps time.Duration with TOML text encoding so the config
// reads natural strings like "30s" or "5m".
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
		return fmt.Errorf("sextantd: parse duration %q: %w", s, err)
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

// DefaultConfig returns a Config populated with sextant's filesystem
// defaults rooted at configDir (typically ~/.config/sextant/) and
// dataDir (typically ~/.local/share/sextant/). Use this directly to
// bootstrap a fresh install; the result already passes Resolve.
func DefaultConfig(configDir, dataDir string) Config {
	return Config{
		Daemon: DaemonConfig{
			ControlSocket:          filepath.Join(dataDir, "sextantd.sock"),
			ShutdownTimeout:        Duration(30 * time.Second),
			RestartBackoffInitial:  Duration(1 * time.Second),
			RestartBackoffMax:      Duration(5 * time.Minute),
			RestartQuarantineAfter: 5,
		},
		CA: CAConfig{
			KeyPath: filepath.Join(configDir, "ca.key"),
			PubPath: filepath.Join(configDir, "ca.pub"),
		},
		NATS: NATSConfig{
			DataDir:       filepath.Join(dataDir, "nats"),
			ListenHost:    "127.0.0.1",
			ListenPort:    0,
			OperatorCreds: filepath.Join(configDir, "operator.creds"),
		},
		ClickHouse: ClickHouseConfig{
			DataDir:      filepath.Join(dataDir, "clickhouse"),
			ListenHost:   "127.0.0.1",
			HTTPPort:     0,
			TCPPort:      0,
			Database:     "sextant",
			User:         "sextant",
			PasswordFile: filepath.Join(configDir, "clickhouse.password"),
		},
		MCP: MCPConfig{
			HTTPHost:    "127.0.0.1",
			HTTPPort:    5172,
			StdioSocket: filepath.Join(dataDir, "sextantd-mcp.sock"),
		},
		Paths: PathsConfig{
			TemplatesDir: filepath.Join(configDir, "templates"),
			ClientConfig: filepath.Join(configDir, "client.toml"),
			RuntimeFile:  filepath.Join(dataDir, "runtime.json"),
			DataDir:      dataDir,
			ConfigDir:    configDir,
		},
	}
}

// DefaultPaths returns the canonical (config_dir, data_dir) pair against
// the current user's home. Wraps os.UserHomeDir so callers get a single
// error and a consistent layout.
func DefaultPaths() (configDir, dataDir string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("sextantd: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "sextant"),
		filepath.Join(home, ".local", "share", "sextant"),
		nil
}

// LoadConfig reads a TOML file from path, expands ~/ in every path field,
// applies defaults for missing fields, and returns the resolved Config.
func LoadConfig(path string) (Config, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(expanded) //nolint:gosec // operator-controlled path
	if err != nil {
		return Config{}, fmt.Errorf("sextantd: read %s: %w", expanded, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("sextantd: parse %s: %w", expanded, err)
	}
	out, err := cfg.Resolve()
	if err != nil {
		return Config{}, err
	}
	return out, nil
}

// SaveConfig writes cfg to path as TOML with mode 0600. The parent dir
// must already exist.
func SaveConfig(path string, cfg Config) error {
	raw, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("sextantd: marshal config: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("sextantd: write %s: %w", path, err)
	}
	return nil
}

// Resolve expands ~/ in every path field and asserts required fields are
// set. It does not mutate the receiver.
func (c Config) Resolve() (Config, error) {
	out := c
	// Fill defaults if missing.
	if out.Daemon.ShutdownTimeout <= 0 {
		out.Daemon.ShutdownTimeout = Duration(30 * time.Second)
	}
	if out.Daemon.RestartBackoffInitial <= 0 {
		out.Daemon.RestartBackoffInitial = Duration(1 * time.Second)
	}
	if out.Daemon.RestartBackoffMax <= 0 {
		out.Daemon.RestartBackoffMax = Duration(5 * time.Minute)
	}
	if out.Daemon.RestartQuarantineAfter <= 0 {
		out.Daemon.RestartQuarantineAfter = 5
	}
	if out.NATS.ListenHost == "" {
		out.NATS.ListenHost = "127.0.0.1"
	}
	if out.ClickHouse.ListenHost == "" {
		out.ClickHouse.ListenHost = "127.0.0.1"
	}
	if out.ClickHouse.Database == "" {
		out.ClickHouse.Database = "sextant"
	}
	if out.ClickHouse.User == "" {
		out.ClickHouse.User = "sextant"
	}
	if out.MCP.HTTPHost == "" {
		out.MCP.HTTPHost = "127.0.0.1"
	}
	// HTTPPort: 0 = kernel-picked (used by tests). The spec default 5172
	// is applied at DefaultConfig time, not here, so a test that
	// explicitly serializes `http_port = 0` doesn't get silently
	// reverted to 5172 on Load.

	pathFields := []*string{
		&out.Daemon.ControlSocket,
		&out.CA.KeyPath, &out.CA.PubPath,
		&out.NATS.DataDir, &out.NATS.OperatorCreds, &out.NATS.LogFile,
		&out.ClickHouse.DataDir, &out.ClickHouse.PasswordFile, &out.ClickHouse.LogFile,
		&out.MCP.StdioSocket,
		&out.Paths.TemplatesDir, &out.Paths.ClientConfig, &out.Paths.RuntimeFile,
		&out.Paths.ConfigDir, &out.Paths.DataDir,
	}
	for _, p := range pathFields {
		if *p == "" {
			continue
		}
		expanded, err := expandHome(*p)
		if err != nil {
			return Config{}, err
		}
		*p = expanded
	}

	// Required-field guards.
	if out.Daemon.ControlSocket == "" {
		return Config{}, fmt.Errorf("sextantd: daemon.control_socket is required")
	}
	if out.CA.KeyPath == "" || out.CA.PubPath == "" {
		return Config{}, fmt.Errorf("sextantd: ca.key_path and ca.pub_path are required")
	}
	if out.NATS.DataDir == "" {
		return Config{}, fmt.Errorf("sextantd: nats.data_dir is required")
	}
	if out.NATS.OperatorCreds == "" {
		return Config{}, fmt.Errorf("sextantd: nats.operator_creds is required")
	}
	if out.ClickHouse.DataDir == "" {
		return Config{}, fmt.Errorf("sextantd: clickhouse.data_dir is required")
	}
	if out.Paths.TemplatesDir == "" {
		return Config{}, fmt.Errorf("sextantd: paths.templates_dir is required")
	}
	if out.Paths.ClientConfig == "" {
		return Config{}, fmt.Errorf("sextantd: paths.client_config is required")
	}
	if out.Paths.RuntimeFile == "" {
		return Config{}, fmt.Errorf("sextantd: paths.runtime_file is required")
	}
	if out.MCP.StdioSocket == "" {
		return Config{}, fmt.Errorf("sextantd: mcp.stdio_socket is required")
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
		return "", fmt.Errorf("sextantd: resolve home: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

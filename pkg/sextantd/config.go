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
	Shipper    ShipperConfig    `toml:"shipper"`
	Paths      PathsConfig      `toml:"paths"`
	Worktree   WorktreeConfig   `toml:"worktree"`
}

// ShipperConfig governs sextantd's supervision of the sextant-shipper
// subprocess. See specs/components/sextantd.md §"Startup sequence" and
// specs/components/shipper.md §"Wire-up to sextantd".
//
//   - AutoSupervise gates whether the daemon spawns sextant-shipper at
//     startup. Default true (set by DefaultConfig and Resolve). When
//     false, the daemon boots without a shipper and the operator runs
//     `sextant-shipper` standalone.
//   - BinaryPath optionally overrides the path to the sextant-shipper
//     executable. Empty triggers the default resolution: same directory
//     as the running sextantd binary, then a PATH lookup.
//   - ConfigPath optionally overrides the shipper.toml path. Empty
//     defaults to <config_dir>/shipper.toml.
//   - LogFile optionally redirects the shipper's stdout/stderr to a
//     file. Empty routes to /dev/null.
type ShipperConfig struct {
	AutoSupervise *bool  `toml:"auto_supervise"`
	BinaryPath    string `toml:"binary_path"`
	ConfigPath    string `toml:"config_path"`
	LogFile       string `toml:"log_file"`
}

// AutoSuperviseEnabled returns the effective auto_supervise flag. nil
// (omitted in TOML) defaults to true so a fresh sextantd.toml without a
// [shipper] block still gets shipper supervision.
func (s ShipperConfig) AutoSuperviseEnabled() bool {
	if s.AutoSupervise == nil {
		return true
	}
	return *s.AutoSupervise
}

// WorktreeConfig governs the M14 worktree manager.
//
//   - RepoRoot is the main worktree path (the operator's checked-out
//     repository). When empty, the daemon skips wiring the worktree
//     surface; this is the M14 transitional state where the daemon
//     may run pre-checkout.
//   - WorktreesRoot is the parent directory where per-task worktrees
//     land. Default: <data_dir>/worktrees (i.e.
//     ~/.local/share/sextant/worktrees/). The operator may override to
//     match the conventions/git-workflow.md default
//     ~/dev/sextant-worktrees/.
//   - PruneInterval is how often the pruner tick fires inside sextantd.
//     Zero / omitted falls back to DefaultPruneInterval (6h) — matches
//     the spec in conventions/git-workflow.md "Disk hygiene".
//   - ArchiveRoot is where archived worktrees land. Zero / omitted
//     falls back to <data_dir>/worktree-archive.
type WorktreeConfig struct {
	RepoRoot      string   `toml:"repo_root"`
	WorktreesRoot string   `toml:"worktrees_root"`
	PruneInterval Duration `toml:"prune_interval"`
	ArchiveRoot   string   `toml:"archive_root"`
}

// DefaultPruneInterval is how often the daemon ticks the worktree
// pruner. The spec doesn't pin a number; 6h is a balance between
// "catch idle worktrees within a day" and "don't churn the disk
// every few minutes". Operators can override via [worktree]
// prune_interval = "..." in sextantd.toml.
const DefaultPruneInterval = 6 * time.Hour

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
		Shipper: ShipperConfig{
			AutoSupervise: boolPtr(true),
			ConfigPath:    filepath.Join(configDir, "shipper.toml"),
		},
		Paths: PathsConfig{
			TemplatesDir: filepath.Join(configDir, "templates"),
			ClientConfig: filepath.Join(configDir, "client.toml"),
			RuntimeFile:  filepath.Join(dataDir, "runtime.json"),
			DataDir:      dataDir,
			ConfigDir:    configDir,
		},
		Worktree: WorktreeConfig{
			// RepoRoot is intentionally empty in the default — see
			// WorktreeConfig docs. The operator sets it when their
			// checkout exists.
			RepoRoot:      "",
			WorktreesRoot: filepath.Join(dataDir, "worktrees"),
			PruneInterval: Duration(DefaultPruneInterval),
			ArchiveRoot:   filepath.Join(dataDir, "worktree-archive"),
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

	// AutoSupervise defaults to true on a missing [shipper] block so
	// upgrades from pre-shipper-supervisor sextantd.toml get the new
	// behavior. Explicit `auto_supervise = false` is preserved.
	if out.Shipper.AutoSupervise == nil {
		out.Shipper.AutoSupervise = boolPtr(true)
	}
	// ConfigPath default kicks in here (not at DefaultConfig) so a
	// hand-rolled sextantd.toml that omits the [shipper] block still
	// gets the canonical <config_dir>/shipper.toml path. Skip when
	// ConfigDir hasn't been set (some test paths leave it empty).
	if out.Shipper.ConfigPath == "" && out.Paths.ConfigDir != "" {
		out.Shipper.ConfigPath = filepath.Join(out.Paths.ConfigDir, "shipper.toml")
	}

	// Worktree-pruner defaults: zero/omitted interval falls back to the
	// canonical 6h spec, archive root falls back to
	// <data_dir>/worktree-archive when DataDir is known.
	if out.Worktree.PruneInterval.AsDuration() <= 0 {
		out.Worktree.PruneInterval = Duration(DefaultPruneInterval)
	}
	if out.Worktree.ArchiveRoot == "" && out.Paths.DataDir != "" {
		out.Worktree.ArchiveRoot = filepath.Join(out.Paths.DataDir, "worktree-archive")
	}

	pathFields := []*string{
		&out.Daemon.ControlSocket,
		&out.CA.KeyPath, &out.CA.PubPath,
		&out.NATS.DataDir, &out.NATS.OperatorCreds, &out.NATS.LogFile,
		&out.ClickHouse.DataDir, &out.ClickHouse.PasswordFile, &out.ClickHouse.LogFile,
		&out.MCP.StdioSocket,
		&out.Shipper.BinaryPath, &out.Shipper.ConfigPath, &out.Shipper.LogFile,
		&out.Paths.TemplatesDir, &out.Paths.ClientConfig, &out.Paths.RuntimeFile,
		&out.Paths.ConfigDir, &out.Paths.DataDir,
		&out.Worktree.RepoRoot, &out.Worktree.WorktreesRoot, &out.Worktree.ArchiveRoot,
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

// boolPtr returns a pointer to b. Used so ShipperConfig.AutoSupervise
// can distinguish "omitted" from "explicitly false".
func boolPtr(b bool) *bool { return &b }

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

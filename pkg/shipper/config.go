package shipper

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the parsed shape of ~/.config/sextant/shipper.toml. See
// specs/components/shipper.md §"Config file" for the schema. Zero value
// is invalid; build via DefaultConfig or LoadConfig.
type Config struct {
	NATS       NATSConfig       `toml:"nats"`
	ClickHouse ClickHouseConfig `toml:"clickhouse"`
	Buffer     BufferConfig     `toml:"buffer"`
	Batch      BatchConfig      `toml:"batch"`
	Shipper    ShipperConfig    `toml:"shipper"`
}

// NATSConfig holds NATS connection details.
type NATSConfig struct {
	// URL is the NATS server URL. Empty means "read from runtime.json".
	URL string `toml:"url"`
	// OperatorCreds is the path to operator.creds (TOML written by
	// sextant init). Required as the credential fallback.
	OperatorCreds string `toml:"operator_creds"`
	// DaemonUser / DaemonPassword are the privileged daemon NATS principal
	// (feat-ctl-f0), populated from runtime.json by MergeRuntime — never
	// from TOML. The shipper consumes every stream via JetStream and so
	// needs the unrestricted daemon principal. When empty (older runtime
	// file or a standalone shipper), New falls back to OperatorCreds.
	DaemonUser     string `toml:"-"`
	DaemonPassword string `toml:"-"`
}

// ClickHouseConfig holds ClickHouse connection details.
type ClickHouseConfig struct {
	// Addr is the host:port of the ClickHouse native TCP listener.
	// Empty means "read from runtime.json".
	Addr string `toml:"addr"`
	// Database is the target database name.
	Database string `toml:"database"`
	// User is the SQL user.
	User string `toml:"user"`
	// PasswordFile is the path to the plain-text password file.
	PasswordFile string `toml:"password_file"`
}

// BufferConfig governs the BoltDB spillover.
type BufferConfig struct {
	// Dir is the directory holding the BoltDB file. Required.
	Dir string `toml:"dir"`
	// HardCapBytes is the maximum BoltDB file size before fail-closed.
	// Default 10 GiB.
	HardCapBytes int64 `toml:"hard_cap_bytes"`
}

// BatchConfig governs per-table batching.
type BatchConfig struct {
	// MaxEvents triggers a flush when reached. Default 1000.
	MaxEvents int `toml:"max_events"`
	// FlushInterval triggers a flush when elapsed. Default 100ms.
	FlushInterval Duration `toml:"flush_interval"`
	// AckWait is the JetStream AckWait. Must exceed FlushInterval +
	// ClickHouse write time. Default 30s.
	AckWait Duration `toml:"ack_wait"`
}

// ShipperConfig governs the shipper itself.
type ShipperConfig struct {
	// DegradedMode is "" (fail-closed, default) or "drop_oldest".
	DegradedMode string `toml:"degraded_mode"`
	// MetricsInterval is the period for publishing shipper telemetry.
	// Default 5s.
	MetricsInterval Duration `toml:"metrics_interval"`
	// ServiceName labels the shipper's own telemetry. Default
	// "sextant-shipper".
	ServiceName string `toml:"service_name"`
	// HostID overrides the host id used in metrics subject paths.
	// Empty means os.Hostname().
	HostID string `toml:"host_id"`
}

// Duration wraps time.Duration with TOML text encoding so the config
// reads natural strings like "100ms" or "30s".
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
		return fmt.Errorf("shipper: parse duration %q: %w", s, err)
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

const (
	// DegradedModeFailClosed is the default: shipper exits non-zero on
	// hard-cap hit, no silent drops.
	DegradedModeFailClosed = ""
	// DegradedModeDropOldest tells the shipper to drop the oldest
	// spillover entries instead of failing closed. Every drop emits an
	// `audit.shipper_drop` audit envelope.
	DegradedModeDropOldest = "drop_oldest"

	// DefaultHardCapBytes is the BoltDB hard cap. 10 GiB.
	DefaultHardCapBytes int64 = 10 << 30
	// DefaultMaxEvents is the per-table flush threshold by row count.
	DefaultMaxEvents = 1000
	// DefaultFlushInterval is the per-table flush threshold by time.
	DefaultFlushInterval = 100 * time.Millisecond
	// DefaultAckWait is the JetStream AckWait.
	DefaultAckWait = 30 * time.Second
	// DefaultMetricsInterval is how often the shipper publishes its
	// own metrics.
	DefaultMetricsInterval = 5 * time.Second
	// DefaultServiceName is the OTel service.name on emitted metrics.
	DefaultServiceName = "sextant-shipper"
)

// DefaultConfig returns a Config populated with sextant's filesystem
// defaults rooted at the operator's home dir. Empty NATS / ClickHouse
// addresses (which require runtime.json) are filled in later by the
// caller.
func DefaultConfig(configDir, dataDir string) Config {
	return Config{
		NATS: NATSConfig{
			URL:           "",
			OperatorCreds: filepath.Join(configDir, "operator.creds"),
		},
		ClickHouse: ClickHouseConfig{
			Addr:         "",
			Database:     "sextant",
			User:         "sextant",
			PasswordFile: filepath.Join(configDir, "clickhouse.password"),
		},
		Buffer: BufferConfig{
			Dir:          filepath.Join(dataDir, "shipper-buffer"),
			HardCapBytes: DefaultHardCapBytes,
		},
		Batch: BatchConfig{
			MaxEvents:     DefaultMaxEvents,
			FlushInterval: Duration(DefaultFlushInterval),
			AckWait:       Duration(DefaultAckWait),
		},
		Shipper: ShipperConfig{
			DegradedMode:    DegradedModeFailClosed,
			MetricsInterval: Duration(DefaultMetricsInterval),
			ServiceName:     DefaultServiceName,
		},
	}
}

// DefaultConfigPath returns ~/.config/sextant/shipper.toml.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("shipper: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "sextant", "shipper.toml"), nil
}

// LoadConfig reads a TOML file from path, expands ~/ in every path
// field, fills defaults, and validates.
func LoadConfig(path string) (Config, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(expanded) //nolint:gosec // operator-controlled path
	if err != nil {
		return Config{}, fmt.Errorf("shipper: read %s: %w", expanded, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("shipper: parse %s: %w", expanded, err)
	}
	out, err := cfg.Resolve()
	if err != nil {
		return Config{}, err
	}
	return out, nil
}

// SaveConfig writes cfg to path as TOML with mode 0600.
func SaveConfig(path string, cfg Config) error {
	raw, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("shipper: marshal config: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("shipper: write %s: %w", path, err)
	}
	return nil
}

// Resolve expands ~/ in every path field, applies defaults for missing
// fields, and validates required fields. Does not mutate the receiver.
func (c Config) Resolve() (Config, error) {
	out := c
	if out.ClickHouse.Database == "" {
		out.ClickHouse.Database = "sextant"
	}
	if out.ClickHouse.User == "" {
		out.ClickHouse.User = "sextant"
	}
	if out.Buffer.HardCapBytes <= 0 {
		out.Buffer.HardCapBytes = DefaultHardCapBytes
	}
	if out.Batch.MaxEvents <= 0 {
		out.Batch.MaxEvents = DefaultMaxEvents
	}
	if out.Batch.FlushInterval <= 0 {
		out.Batch.FlushInterval = Duration(DefaultFlushInterval)
	}
	if out.Batch.AckWait <= 0 {
		out.Batch.AckWait = Duration(DefaultAckWait)
	}
	if out.Shipper.MetricsInterval <= 0 {
		out.Shipper.MetricsInterval = Duration(DefaultMetricsInterval)
	}
	if out.Shipper.ServiceName == "" {
		out.Shipper.ServiceName = DefaultServiceName
	}

	switch out.Shipper.DegradedMode {
	case DegradedModeFailClosed, DegradedModeDropOldest:
	default:
		return Config{}, fmt.Errorf("shipper: invalid degraded_mode %q (want \"\" or \"drop_oldest\")",
			out.Shipper.DegradedMode)
	}

	pathFields := []*string{
		&out.NATS.OperatorCreds,
		&out.ClickHouse.PasswordFile,
		&out.Buffer.Dir,
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

	if out.NATS.OperatorCreds == "" {
		return Config{}, fmt.Errorf("shipper: nats.operator_creds is required")
	}
	if out.ClickHouse.PasswordFile == "" {
		return Config{}, fmt.Errorf("shipper: clickhouse.password_file is required")
	}
	if out.Buffer.Dir == "" {
		return Config{}, fmt.Errorf("shipper: buffer.dir is required")
	}
	if out.Batch.FlushInterval >= out.Batch.AckWait {
		return Config{}, fmt.Errorf("shipper: batch.ack_wait (%s) must exceed batch.flush_interval (%s)",
			out.Batch.AckWait.AsDuration(), out.Batch.FlushInterval.AsDuration())
	}

	return out, nil
}

// RuntimeAddrs is the subset of sextantd's runtime.json relevant to the
// shipper. We mirror the field names so a future migration to a shared
// package is trivial.
type RuntimeAddrs struct {
	NATSAddr           string `json:"nats_addr"`
	NATSDaemonUser     string `json:"nats_daemon_user"`
	NATSDaemonPassword string `json:"nats_daemon_password"`
	ClickHouseTCP      string `json:"clickhouse_tcp"`
}

// MergeRuntime fills empty NATS URL and ClickHouse Addr from
// runtime.json. If both the config and runtime.json are empty for a
// required address, returns an error so startup fails fast.
//
// The runtime file is best-effort: when it's missing and the config
// already has explicit addresses, no error. When the file exists but
// can't be parsed, return the parse error — silent fallback is too
// surprising.
func (c Config) MergeRuntime(runtimePath string) (Config, error) {
	out := c
	// Even when both addresses are configured explicitly we still want the
	// daemon credential from runtime.json (feat-ctl-f0), so only short-
	// circuit when there is nothing left to fill.
	if out.NATS.URL != "" && out.ClickHouse.Addr != "" && out.NATS.DaemonUser != "" {
		return out, nil
	}
	raw, err := os.ReadFile(runtimePath) //nolint:gosec // operator-controlled path
	if err != nil {
		if os.IsNotExist(err) {
			if out.NATS.URL != "" && out.ClickHouse.Addr != "" {
				// Addresses came from explicit config; the missing runtime
				// file just means we fall back to operator.creds for auth.
				return out, nil
			}
			return out, fmt.Errorf("shipper: nats.url/clickhouse.addr empty and runtime file missing at %s", runtimePath)
		}
		return Config{}, fmt.Errorf("shipper: read runtime file %s: %w", runtimePath, err)
	}
	var rt RuntimeAddrs
	if err := json.Unmarshal(raw, &rt); err != nil {
		return Config{}, fmt.Errorf("shipper: parse runtime file %s: %w", runtimePath, err)
	}
	if out.NATS.URL == "" {
		if rt.NATSAddr == "" {
			return Config{}, fmt.Errorf("shipper: runtime file %s missing nats_addr", runtimePath)
		}
		out.NATS.URL = "nats://" + rt.NATSAddr
	}
	// Daemon principal: the privileged credential the shipper needs to
	// consume every stream via JetStream. Empty in older runtime files;
	// New falls back to operator.creds in that case.
	if out.NATS.DaemonUser == "" {
		out.NATS.DaemonUser = rt.NATSDaemonUser
		out.NATS.DaemonPassword = rt.NATSDaemonPassword
	}
	if out.ClickHouse.Addr == "" {
		if rt.ClickHouseTCP == "" {
			return Config{}, fmt.Errorf("shipper: runtime file %s missing clickhouse_tcp", runtimePath)
		}
		out.ClickHouse.Addr = rt.ClickHouseTCP
	}
	return out, nil
}

// HostID resolves the host id to use in metrics subject paths. Returns
// the config override when non-empty; otherwise os.Hostname() with a
// "host" fallback when hostname lookup fails.
func (c Config) HostID() string {
	if c.Shipper.HostID != "" {
		return c.Shipper.HostID
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "host"
	}
	// Strip the FQDN tail; metric subjects are short.
	if idx := strings.IndexByte(h, '.'); idx > 0 {
		h = h[:idx]
	}
	return sanitizeSubjectToken(h)
}

// sanitizeSubjectToken replaces NATS-illegal characters in a subject
// token. Hostnames can contain dashes (legal) and underscores (legal)
// but periods would split the subject hierarchy unexpectedly.
func sanitizeSubjectToken(s string) string {
	if s == "" {
		return "host"
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '*', '>', ' ':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func expandHome(p string) (string, error) {
	if !strings.HasPrefix(p, "~/") && p != "~" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("shipper: resolve home: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// validateAddr keeps a simple sanity check on URL/host:port strings so
// typos surface at startup rather than at first publish.
func validateAddr(label, s string) error {
	if s == "" {
		return fmt.Errorf("shipper: %s is empty", label)
	}
	// host:port form (clickhouse.addr)
	if !strings.Contains(s, "://") {
		if _, _, err := net.SplitHostPort(s); err != nil {
			return fmt.Errorf("shipper: %s not host:port: %w", label, err)
		}
		return nil
	}
	// nats://host:port form
	return nil
}

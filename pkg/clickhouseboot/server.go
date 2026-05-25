package clickhouseboot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	chgo "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Server represents a running clickhouse-server subprocess.
type Server struct {
	cfg     Config
	cmd     *exec.Cmd
	cfgPath string
	logFile *os.File

	// waitDone closes after the subprocess exits; waitErr holds the
	// cached exit error. Single Wait goroutine owns cmd.Wait().
	waitDone chan struct{}
	mu       sync.Mutex
	waitErr  error
}

// Start writes a clickhouse-server config XML to cfg.DataDir, exec's
// `clickhouse server -C <cfg>`, and waits up to cfg.StartupTimeout for a
// successful `SELECT 1` over the native TCP listener.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	cfg, err := cfg.validateAndFill()
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"data", "tmp", "user_files", "format_schemas", "logs"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, sub), 0o750); err != nil {
			return nil, fmt.Errorf("clickhouseboot: mkdir %s: %w", sub, err)
		}
	}

	cfgPath := filepath.Join(cfg.DataDir, "config.xml")
	if err := writeConfigFile(cfgPath, cfg); err != nil {
		return nil, fmt.Errorf("clickhouseboot: write config: %w", err)
	}

	cmd := exec.CommandContext(ctx, cfg.ClickHouseBinary, "server", "-C", cfgPath) //nolint:gosec // operator-controlled binary
	cmd.Dir = cfg.DataDir

	// stdout/stderr must be real files (or nil), not io.Discard:
	// exec.Cmd spawns a copier goroutine when Stdout is a non-File
	// io.Writer, and that goroutine blocks Wait() on pipe close.
	// ClickHouse's leaf threads keep stdout open well after exit,
	// which deadlocks Wait. Route to a real file in either branch.
	var logFile *os.File
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640) //nolint:gosec // operator-config controlled
		if err != nil {
			return nil, fmt.Errorf("clickhouseboot: open log file %s: %w", cfg.LogFile, err)
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	} else {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("clickhouseboot: open %s: %w", os.DevNull, err)
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// exec.CommandContext's default Cancel callback calls
	// cmd.Process.Kill, which SIGKILLs only the leader pid — the same
	// leak vector 2903609 fixed for the explicit Stop path. clickhouse
	// forks a watchdog child that lives in the leader's process group;
	// if ctx is canceled (e.g. daemon shutdown via main ctx cancel),
	// the default Cancel orphans that child as PPID=1. Override Cancel
	// to signal the whole process group instead.
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd, syscall.SIGKILL)
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, fmt.Errorf("clickhouseboot: start clickhouse-server: %w", err)
	}

	s := &Server{
		cfg:      cfg,
		cmd:      cmd,
		cfgPath:  cfgPath,
		logFile:  logFile,
		waitDone: make(chan struct{}),
	}
	// Single owner of cmd.Wait().
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waitErr = err
		s.mu.Unlock()
		close(s.waitDone)
	}()
	if err := s.waitReady(ctx); err != nil {
		// Detach cleanup from caller ctx.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		_ = s.Stop(stopCtx) //nolint:contextcheck // intentional detach
		stopCancel()
		return nil, err
	}
	return s, nil
}

// TCPAddress returns the host:port string for the native protocol.
func (s *Server) TCPAddress() string {
	return net.JoinHostPort(s.cfg.ListenHost, strconv.Itoa(s.cfg.TCPPort))
}

// HTTPAddress returns the host:port string for the HTTP API.
func (s *Server) HTTPAddress() string {
	return net.JoinHostPort(s.cfg.ListenHost, strconv.Itoa(s.cfg.HTTPPort))
}

// Database returns the default database name.
func (s *Server) Database() string { return s.cfg.Database }

// User returns the SQL user name.
func (s *Server) User() string { return s.cfg.User }

// Password returns the SQL user password. Sensitive.
func (s *Server) Password() string { return s.cfg.Password }

// ConfigPath returns the rendered server config path.
func (s *Server) ConfigPath() string { return s.cfgPath }

// DSN returns a database/sql DSN suitable for sql.Open("clickhouse", DSN).
// Includes credentials.
func (s *Server) DSN() string {
	return fmt.Sprintf("clickhouse://%s:%s@%s/%s",
		s.cfg.User, s.cfg.Password, s.TCPAddress(), s.cfg.Database)
}

// Open returns a driver.Conn authenticated as the sextant user. The
// caller owns Close.
func (s *Server) Open(ctx context.Context) (driver.Conn, error) {
	opts := &chgo.Options{
		Addr: []string{s.TCPAddress()},
		Auth: chgo.Auth{
			Database: s.cfg.Database,
			Username: s.cfg.User,
			Password: s.cfg.Password,
		},
		DialTimeout:     5 * time.Second,
		ReadTimeout:     30 * time.Second,
		ConnMaxLifetime: 1 * time.Hour,
	}
	conn, err := chgo.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouseboot: open conn: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouseboot: ping: %w", err)
	}
	return conn, nil
}

// OpenDB returns a *sql.DB bound to clickhouse-go's driver.
func (s *Server) OpenDB() *sql.DB {
	opts := &chgo.Options{
		Addr: []string{s.TCPAddress()},
		Auth: chgo.Auth{
			Database: s.cfg.Database,
			Username: s.cfg.User,
			Password: s.cfg.Password,
		},
	}
	return chgo.OpenDB(opts)
}

// Stop signals the subprocess to exit gracefully via SIGTERM, then
// SIGKILL after cfg.ShutdownTimeout. If the subprocess has already
// exited (e.g. observed via Done), Stop returns the cached status.
func (s *Server) Stop(ctx context.Context) error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	// Fast path: subprocess already exited.
	select {
	case <-s.waitDone:
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	default:
	}

	// SIGTERM the whole process group, not just the leader.
	// clickhouse-server forks an internal worker that stays in the
	// leader's process group; signaling only the leader leaves the
	// worker behind as a PPID=1 orphan. Setpgid:true at Start time
	// makes the leader its own group, so the negative-pid kill
	// reaches every descendant in one syscall.
	if err := signalProcessGroup(s.cmd, syscall.SIGTERM); err != nil {
		return fmt.Errorf("clickhouseboot: SIGTERM pgroup: %w", err)
	}

	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case <-s.waitDone:
		// SIGKILL the group on the success path too — Wait reaps the
		// leader, but a misbehaving worker that ignored SIGTERM would
		// still be running. Best-effort; ESRCH is normal here.
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	case <-time.After(timeout):
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		<-s.waitDone
		s.closeLog()
		return fmt.Errorf("clickhouseboot: did not stop within %s; sent SIGKILL", timeout)
	case <-ctx.Done():
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		<-s.waitDone
		s.closeLog()
		return ctx.Err()
	}
}

// signalProcessGroup sends sig to every process in the process group
// led by cmd's leader. Requires Setpgid:true at Start time so the
// leader's PGID equals its PID; we then signal -PID per POSIX
// convention. ESRCH (group already gone) folds to nil.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}

// Done returns a channel that closes when the underlying
// clickhouse-server subprocess exits. After observing Done, callers can
// inspect ExitErr.
func (s *Server) Done() <-chan struct{} {
	return s.waitDone
}

// PID returns the subprocess PID, or 0 if the subprocess is not started.
func (s *Server) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// ExitErr returns the error from cmd.Wait(). Only meaningful after Done
// has closed; returns nil if the subprocess is still running.
func (s *Server) ExitErr() error {
	select {
	case <-s.waitDone:
		return s.cachedWaitErr()
	default:
		return nil
	}
}

func (s *Server) cachedWaitErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}

// classifyExit folds expected signal-induced exits into nil. ClickHouse
// returns non-zero on SIGTERM/SIGKILL/SIGINT; treat any *exec.ExitError
// as "subprocess exited because we said so" — natsboot uses the same
// policy. Non-ExitError errors propagate.
func classifyExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return fmt.Errorf("clickhouseboot: wait: %w", err)
}

func (s *Server) closeLog() {
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

// waitReady polls the server with SELECT 1 until success or timeout.
func (s *Server) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(s.cfg.StartupTimeout)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("clickhouseboot: ctx canceled waiting for ready: %w", ctx.Err())
		}
		// Bail early if the subprocess has already died. Observe
		// waitDone (owned by the single Wait goroutine) instead of
		// poking at cmd.ProcessState to avoid racing the Wait writer.
		select {
		case <-s.waitDone:
			return fmt.Errorf("clickhouseboot: clickhouse-server exited during startup: %w", s.cachedWaitErr())
		default:
		}
		probeCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		conn, err := s.Open(probeCtx)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("clickhouseboot: did not become ready within %s: %w",
				s.cfg.StartupTimeout, lastErr)
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// writeConfigFile renders cfg to path with mode 0o600 (password inside).
func writeConfigFile(path string, cfg Config) error {
	tmpl := template.Must(template.New("ch").Parse(configTemplate))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // path is config-controlled by clickhouseboot
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // close after Truncate intentional

	data := map[string]string{
		"DataDir":      strings.TrimRight(cfg.DataDir, "/") + "/",
		"ListenHost":   cfg.ListenHost,
		"HTTPPort":     strconv.Itoa(cfg.HTTPPort),
		"TCPPort":      strconv.Itoa(cfg.TCPPort),
		"User":         cfg.User,
		"PasswordSHA":  sha256Hex(cfg.Password),
		"Database":     cfg.Database,
		"LogPath":      filepath.Join(cfg.DataDir, "logs", "clickhouse-server.log"),
		"ErrorLogPath": filepath.Join(cfg.DataDir, "logs", "clickhouse-server.err.log"),
	}
	return tmpl.Execute(f, data)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

const configTemplate = `<?xml version="1.0"?>
<clickhouse>
  <logger>
    <level>warning</level>
    <log>{{ .LogPath }}</log>
    <errorlog>{{ .ErrorLogPath }}</errorlog>
    <size>100M</size>
    <count>3</count>
  </logger>
  <listen_host>{{ .ListenHost }}</listen_host>
  <http_port>{{ .HTTPPort }}</http_port>
  <tcp_port>{{ .TCPPort }}</tcp_port>
  <interserver_http_port>0</interserver_http_port>
  <path>{{ .DataDir }}data/</path>
  <tmp_path>{{ .DataDir }}tmp/</tmp_path>
  <user_files_path>{{ .DataDir }}user_files/</user_files_path>
  <format_schema_path>{{ .DataDir }}format_schemas/</format_schema_path>
  <mark_cache_size>536870912</mark_cache_size>
  <max_concurrent_queries>32</max_concurrent_queries>
  <max_connections>64</max_connections>
  <default_profile>default</default_profile>
  <default_database>{{ .Database }}</default_database>
  <users>
    <{{ .User }}>
      <password_sha256_hex>{{ .PasswordSHA }}</password_sha256_hex>
      <networks><ip>127.0.0.1</ip><ip>::1</ip></networks>
      <profile>default</profile>
      <quota>default</quota>
      <access_management>1</access_management>
      <named_collection_control>1</named_collection_control>
    </{{ .User }}>
  </users>
  <profiles>
    <default>
      <max_memory_usage>4000000000</max_memory_usage>
      <load_balancing>random</load_balancing>
    </default>
  </profiles>
  <quotas>
    <default>
      <interval>
        <duration>3600</duration>
        <queries>0</queries>
        <errors>0</errors>
        <result_rows>0</result_rows>
        <read_rows>0</read_rows>
        <execution_time>0</execution_time>
      </interval>
    </default>
  </quotas>
  <distributed_ddl><path>/clickhouse/task_queue/ddl</path></distributed_ddl>
  <openSSL>
    <server>
      <verificationMode>none</verificationMode>
      <disableProtocols>sslv2,sslv3</disableProtocols>
    </server>
    <client>
      <verificationMode>none</verificationMode>
      <disableProtocols>sslv2,sslv3</disableProtocols>
    </client>
  </openSSL>
</clickhouse>
`

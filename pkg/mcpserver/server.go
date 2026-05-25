package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/templates"
)

// toolError is the structured error a tool handler returns to the
// dispatcher. The dispatcher converts it into a CallToolResult with
// IsError + a TextContent body so the client (sidecar / TS SDK) sees a
// proper MCP tool error rather than a transport-level protocol error.
type toolError struct {
	Code    string
	Message string
	Details map[string]any
}

func (e toolError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Config bundles the dependencies and tunables sextantd passes to New.
// Required fields: NATS, CA, From.
type Config struct {
	// NATS is the operator-trusted NATS connection. Tool handlers
	// publish bus envelopes through it.
	NATS *nats.Conn

	// CA is the sextant signing CA used to verify per-incarnation JWTs
	// on the HTTP transport. Loaded by sextantd from
	// ~/.config/sextant/ca.{key,pub}.
	CA *authjwt.CA

	// From is the daemon's address stamped into envelopes the MCP
	// server publishes (audit, tool-published bus messages). Typically
	// `{Kind: AddressDaemon, ID: "daemon-<host>"}`.
	From sextantproto.Address

	// HTTPHost / HTTPPort configure the Streamable HTTP listener.
	// HTTPPort=0 lets the kernel pick a free port (useful in tests);
	// production wires the spec default 5172.
	HTTPHost string
	HTTPPort int

	// StdioSocket is the absolute path to the operator-only Unix
	// socket that carries MCP stdio framing. Empty disables the stdio
	// transport (useful for tests that only exercise the HTTP path).
	StdioSocket string

	// AgentKV resolves agent_definitions KV entries for list_agents /
	// agent_status. Optional in tests that only exercise send_message;
	// nil-checked at tool-invocation time.
	AgentKV handlers.AgentKV

	// QueryDB is the ClickHouse driver.Conn used by query_audit.
	// Optional in tests that only exercise send_message.
	QueryDB handlers.QueryHistoryDB

	// SpawnDeps wires the spawn_agent / kill_agent / prompt_agent tool
	// implementations to the real RPC handlers. Optional: when nil the
	// three tools fall back to the M10 NotImplemented stub so older
	// callers don't crash. The daemon (M11+) always populates this.
	SpawnDeps *SpawnDeps

	// Worktree is the manager backing the M14 worktree_* tools. When
	// nil the tools return ErrCodeInternal (not configured). The
	// daemon (M14+) always populates this.
	Worktree handlers.WorktreeManager

	// Logger receives diagnostic messages. Defaults to log.Default.
	Logger *log.Logger
}

// SpawnDeps bundles the dependencies the spawn_agent / kill_agent /
// prompt_agent tools need. It mirrors handlers.SpawnDeps + KillDeps +
// PromptDeps so callers wire one struct; the MCP server unpacks it into
// per-handler shapes internally.
type SpawnDeps struct {
	Definitions  handlers.AgentMutableKV
	Incarnations handlers.AgentMutableKV
	Templates    templates.KV
	Containers   handlers.ContainerRunner
	CA           *authjwt.CA
	History      handlers.HistoryWriter
	WorkspaceDir string
	Worktree     handlers.WorktreeProvider
	// RepoRoot mirrors handlers.SpawnDeps.RepoRoot. The MCP path
	// forwards it verbatim so the operator-NATS and agent-MCP spawn
	// surfaces produce containers with the same mount set.
	RepoRoot     string
	HostID       string
	NATSURL      string
	NATSUser     string
	NATSPassword string
	MCPURL       string
	Issuer       string
	// TestRunLabel, when non-empty, is forwarded to handlers.SpawnDeps
	// so every spawn via the MCP path stamps sextant.test_run=<value>.
	// Empty in production.
	TestRunLabel string
}

// Server is the in-process MCP server. One Server per daemon. Build
// with New, start with Run (which blocks until ctx is canceled), stop
// with Close. Run is also responsible for binding the HTTP listener and
// stdio socket synchronously before serving, so callers may read
// HTTPAddr / StdioSocketPath as soon as Run has had a chance to start
// (typically: after a small sleep or after observing a readiness
// signal). For tests, prefer the explicit Start+Wait API below.
type Server struct {
	cfg   Config
	mcp   *mcp.Server
	audit *auditPublisher

	// mu guards every field below. Accessed from Run/Start and Close,
	// and read-only by HTTPAddr / StdioSocketPath; the mutex is held
	// briefly so the test-side ready check doesn't see a torn write.
	mu           sync.Mutex
	httpServer   *http.Server
	httpListener net.Listener
	httpReady    chan struct{}
	stdioLn      *net.UnixListener
	stdioSocket  string
	started      bool

	stdioWG sync.WaitGroup
	logger  *log.Logger

	closeOnce sync.Once
	closeErr  error
}

// New wires a fresh MCP server. It does not bind any listeners — call
// Run to start serving.
func New(cfg Config) (*Server, error) {
	if cfg.NATS == nil {
		return nil, fmt.Errorf("mcpserver: Config.NATS is required")
	}
	if cfg.CA == nil {
		return nil, fmt.Errorf("mcpserver: Config.CA is required")
	}
	if cfg.From.Kind == "" {
		return nil, fmt.Errorf("mcpserver: Config.From.Kind is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	s := &Server{
		cfg:       cfg,
		logger:    cfg.Logger,
		httpReady: make(chan struct{}),
		audit: &auditPublisher{
			nc:     cfg.NATS,
			from:   cfg.From,
			logger: cfg.Logger,
		},
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "sextantd",
		Version: "0.1.0",
		Title:   "sextant MCP server",
	}, &mcp.ServerOptions{
		Instructions: "sextant tools — communication, introspection, control, system.",
		Logger:       slog.New(slog.NewTextHandler(noopWriter{}, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	s.mcp = mcpServer
	s.registerTools()
	return s, nil
}

// noopWriter discards every byte. The MCP SDK requires a non-nil logger
// in some code paths; we'd rather route diagnostics through our own
// cfg.Logger than have the SDK's slog tee them onto stderr.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// Start binds both transports synchronously. After Start returns
// successfully, HTTPAddr / StdioSocketPath are safe to read. Start does
// not block — Run is the long-lived blocking entry point.
//
// Calling Start twice is a programming error and returns an error;
// callers normally go through Run, which calls Start internally.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("mcpserver: already started")
	}
	s.started = true
	s.mu.Unlock()

	if err := s.startHTTP(); err != nil {
		return fmt.Errorf("mcpserver: start http: %w", err)
	}
	if s.cfg.StdioSocket != "" {
		if err := s.startStdio(ctx); err != nil {
			// Disconnect from ctx for the rollback shutdown: ctx may
			// already be canceled (that's the path that brought us
			// here), and we still need to close http.Server cleanly
			// before returning.
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = s.shutdownHTTP(rollbackCtx)
			cancel()
			return fmt.Errorf("mcpserver: start stdio: %w", err)
		}
	}
	close(s.httpReady)
	s.logger.Printf("mcpserver: ready (http=%s stdio=%s)", s.HTTPAddr(), s.cfg.StdioSocket)
	return nil
}

// Run starts the server (if not already started) and blocks until ctx
// is canceled or Close is called. Returns nil on clean shutdown.
//
// Concurrency: a separate goroutine may call HTTPAddr / Close while Run
// is blocked on ctx — both paths take the same mutex.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return s.shutdown(ctx)
}

// SetSpawnDeps installs (or replaces) the spawn/kill/prompt backend.
// Safe to call after Start: the dispatcher reads s.cfg.SpawnDeps per
// invocation, so a late-bind is the supported pattern for daemons that
// can't build the dep bag before MCP binds (e.g. when the dep bag
// needs the MCP HTTP URL itself).
func (s *Server) SetSpawnDeps(deps *SpawnDeps) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.SpawnDeps = deps
}

// HTTPAddr returns the actual bound address of the HTTP listener
// (host:port). Returns "" if Start has not completed.
func (s *Server) HTTPAddr() string {
	s.mu.Lock()
	ln := s.httpListener
	s.mu.Unlock()
	if ln == nil {
		return ""
	}
	return ln.Addr().String()
}

// Ready returns a channel that closes once Start has finished binding
// the listeners. Use this in tests to deterministically wait for the
// server to be reachable.
func (s *Server) Ready() <-chan struct{} { return s.httpReady }

// HTTPURL returns the fully-qualified Streamable HTTP endpoint URL,
// including the `/mcp` path the sidecar connects to.
func (s *Server) HTTPURL() string {
	addr := s.HTTPAddr()
	if addr == "" {
		return ""
	}
	return "http://" + addr + "/mcp"
}

// StdioSocketPath returns the actual filesystem path the stdio listener
// is bound to.
func (s *Server) StdioSocketPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stdioSocket
}

// Close stops both transports. Idempotent. Safe to call from a separate
// goroutine while Run is blocked on ctx.
func (s *Server) Close() error {
	return s.CloseCtx(context.Background())
}

// CloseCtx is Close with an explicit shutdown context. The context is
// used as the parent for the bounded deadline applied to http.Server's
// Shutdown call.
func (s *Server) CloseCtx(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.closeErr = s.shutdown(ctx)
	})
	return s.closeErr
}

func (s *Server) shutdown(ctx context.Context) error {
	var errs []error
	if err := s.shutdownHTTP(ctx); err != nil {
		errs = append(errs, err)
	}
	s.mu.Lock()
	ln := s.stdioLn
	sock := s.stdioSocket
	s.stdioLn = nil
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
		// Remove the socket file so a subsequent boot doesn't trip on a
		// stale inode.
		if sock != "" {
			_ = os.Remove(sock)
		}
		s.stdioWG.Wait()
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP (Streamable HTTP) transport — sidecar-facing, JWT-authenticated.
// ---------------------------------------------------------------------------

func (s *Server) startHTTP() error {
	addr := net.JoinHostPort(s.cfg.HTTPHost, fmtPort(s.cfg.HTTPPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		// One MCP Server instance shared across all sessions — tools
		// are stateless w.r.t. sessions, identity comes from
		// per-request TokenInfo.
		return s.mcp
	}, &mcp.StreamableHTTPOptions{
		// Localhost-only bind in M10. Disable the SDK's DNS-rebinding
		// guard so test fixtures hitting 127.0.0.1 with a non-matching
		// Host header still work; the real defense is JWT verification
		// on every request.
		DisableLocalhostProtection: true,
	})
	authedHandler := mcpauth.RequireBearerToken(tokenVerifier(s.cfg.CA), nil)(mcpHandler)

	mux := http.NewServeMux()
	mux.Handle("/mcp", authedHandler)
	mux.Handle("/mcp/", authedHandler)

	httpSrv := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streamable HTTP runs long-lived SSE streams; no write timeout
		IdleTimeout:  120 * time.Second,
	}
	s.mu.Lock()
	s.httpListener = ln
	s.httpServer = httpSrv
	s.mu.Unlock()

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Printf("mcpserver: http.Serve: %v", err)
		}
	}()
	return nil
}

// httpShutdownGrace bounds how long shutdownHTTP will wait for in-flight
// HTTP requests to drain before forcibly closing the server. Streamable
// HTTP keeps long-lived SSE streams open by design (the server's
// WriteTimeout is 0), so a graceful Shutdown can otherwise block until
// the client closes the stream — which on daemon shutdown is "never".
const httpShutdownGrace = 5 * time.Second

func (s *Server) shutdownHTTP(ctx context.Context) error {
	s.mu.Lock()
	httpSrv := s.httpServer
	s.httpServer = nil
	s.httpListener = nil
	s.mu.Unlock()
	if httpSrv == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, httpShutdownGrace)
	defer cancel()
	err := httpSrv.Shutdown(shutdownCtx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("mcpserver: http shutdown: %w", err)
	}
	// Graceful Shutdown timed out — long-lived SSE streams are still
	// holding the server open. Forcibly close every active connection
	// so the daemon's shutdown actually completes; without this, the
	// goroutines spawned by http.Serve leak past daemon exit.
	s.logger.Printf("mcpserver: http graceful shutdown exceeded %s; forcing Close()", httpShutdownGrace)
	if closeErr := httpSrv.Close(); closeErr != nil {
		return fmt.Errorf("mcpserver: http force close after timeout: %w", closeErr)
	}
	return nil
}

func fmtPort(p int) string {
	if p <= 0 {
		return "0"
	}
	return fmtInt(p)
}

func fmtInt(i int) string { return fmt.Sprintf("%d", i) }

// ---------------------------------------------------------------------------
// Stdio (Unix socket) transport — operator-facing, no JWT.
// ---------------------------------------------------------------------------

func (s *Server) startStdio(ctx context.Context) error {
	path := s.cfg.StdioSocket
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir socket parent: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	}
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return fmt.Errorf("resolve unix addr: %w", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod socket %s: %w", path, err)
	}
	s.mu.Lock()
	s.stdioLn = ln
	s.stdioSocket = path
	s.mu.Unlock()
	s.stdioWG.Add(1)
	go func() {
		defer s.stdioWG.Done()
		s.acceptStdio(ctx)
	}()
	return nil
}

func (s *Server) acceptStdio(ctx context.Context) {
	s.mu.Lock()
	ln := s.stdioLn
	s.mu.Unlock()
	if ln == nil {
		return
	}
	s.acceptLoop(ctx, ln.AcceptUnix, s.handleStdioConn)
}

// acceptLoop is acceptStdio's testable core. It drives the
// accept→dispatch loop with bounded backoff on transient accept errors
// so a one-off EMFILE/EAGAIN doesn't kill stdio service for the rest
// of the daemon's lifetime.
//
// The accept function returns either (conn, nil) — caller-supplied
// dispatch fires on conn — or (nil, err). If err is net.ErrClosed (or
// ctx is canceled) the loop returns. Anything else is treated as
// transient: log + sleep with exponential backoff (5ms → 1s cap) +
// continue.
func (s *Server) acceptLoop(
	ctx context.Context,
	accept func() (*net.UnixConn, error),
	dispatch func(context.Context, *net.UnixConn),
) {
	var backoff time.Duration
	const maxBackoff = time.Second
	for {
		c, err := accept()
		if err != nil {
			// Listener closed (shutdown) or context canceled — terminal.
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			// Transient: log, back off, retry. Killing the loop here
			// would silently drop stdio service for the lifetime of the
			// daemon on the first EMFILE/EAGAIN.
			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			s.logger.Printf("mcpserver: stdio accept transient error (retry in %s): %v", backoff, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			continue
		}
		// Successful accept resets the backoff window.
		backoff = 0
		s.stdioWG.Add(1)
		go func(conn *net.UnixConn) {
			defer s.stdioWG.Done()
			dispatch(ctx, conn)
		}(c)
	}
}

// handleStdioConn services one accepted Unix-socket connection as an
// MCP stdio session. Extracted as a method so acceptLoop can be tested
// against a synthetic dispatch func.
func (s *Server) handleStdioConn(ctx context.Context, conn *net.UnixConn) {
	defer conn.Close() //nolint:errcheck // best-effort
	t := &mcp.IOTransport{Reader: conn, Writer: nopCloserWC{conn}}
	session, err := s.mcp.Connect(ctx, t, nil)
	if err != nil {
		s.logger.Printf("mcpserver: stdio connect: %v", err)
		return
	}
	if err := session.Wait(); err != nil {
		s.logger.Printf("mcpserver: stdio session: %v", err)
	}
}

// nopCloserWC wraps a UnixConn as an io.WriteCloser whose Close is a
// no-op — the accept loop owns the underlying conn's lifecycle so the
// IOTransport must not double-close it on shutdown.
type nopCloserWC struct {
	*net.UnixConn
}

func (n nopCloserWC) Close() error { return nil }

// ---------------------------------------------------------------------------
// Tool registration + dispatch.
// ---------------------------------------------------------------------------

// registerTools installs the M10 catalog on the MCP server.
func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolSendMessage,
		Description: "Publish a message to another agent's inbox subject (agents.<to_agent>.inbox).",
	}, wrapHandler(s, ToolSendMessage, s.handleSendMessage))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolBroadcast,
		Description: "Publish a message under broadcast.<subject>.",
	}, wrapHandler(s, ToolBroadcast, s.handleBroadcast))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolListAgents,
		Description: "List known agents, optionally filtered by lifecycle.",
	}, wrapHandler(s, ToolListAgents, s.handleListAgents))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolAgentStatus,
		Description: "Fetch one agent's status by UUID.",
	}, wrapHandler(s, ToolAgentStatus, s.handleAgentStatus))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolQueryAudit,
		Description: "Query the audit log. Returns audit envelopes matching the filter.",
	}, wrapHandler(s, ToolQueryAudit, s.handleQueryAudit))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolSpawnAgent,
		Description: "Spawn a new agent from a template; returns the new agent UUID.",
	}, wrapHandler(s, ToolSpawnAgent, s.handleSpawnAgent))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolKillAgent,
		Description: "Stop a running agent's container and flip its definition back to defined.",
	}, wrapHandler(s, ToolKillAgent, s.handleKillAgent))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolArchiveAgent,
		Description: "Archive an agent so its name is released and re-usable. Stops the live container first if one is running.",
	}, wrapHandler(s, ToolArchiveAgent, s.handleArchiveAgent))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolPromptAgent,
		Description: "Publish a prompt to an agent's inbox subject (agents.<uuid>.inbox).",
	}, wrapHandler(s, ToolPromptAgent, s.handlePromptAgent))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolEmitEvent,
		Description: "Publish a free-form event to sextant.system.<subject>.",
	}, wrapHandler(s, ToolEmitEvent, s.handleEmitEvent))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolGetMetric,
		Description: "Fetch a metric series from ClickHouse. (M10: stubbed.)",
	}, wrapHandler(s, ToolGetMetric, s.handleNotImplemented))

	// M14 worktree tools.
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolWorktreeCreate,
		Description: "Create a new git worktree on a fresh branch off the given base branch.",
	}, wrapHandler(s, ToolWorktreeCreate, s.handleWorktreeCreate))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolWorktreeDestroy,
		Description: "Remove a git worktree and delete its registry entry.",
	}, wrapHandler(s, ToolWorktreeDestroy, s.handleWorktreeDestroy))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolWorktreeList,
		Description: "List every known worktree with status, branch, and ownership.",
	}, wrapHandler(s, ToolWorktreeList, s.handleWorktreeList))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolWorktreeMerge,
		Description: "Merge a worktree's branch into target (default main) under the merge lock.",
	}, wrapHandler(s, ToolWorktreeMerge, s.handleWorktreeMerge))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolWorktreeDiff,
		Description: "Show the diff of a worktree against a target branch (default main).",
	}, wrapHandler(s, ToolWorktreeDiff, s.handleWorktreeDiff))

	// M16: templates_reload. Calls into the same NATS control subject
	// the operator CLI uses (sextant.control.templates_reload) so a
	// CLI-driven reload and an agent-driven reload are byte-for-byte
	// identical paths on the daemon side.
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        ToolTemplatesReload,
		Description: "Re-scan the daemon's templates dir and push every *.toml into NATS KV. Returns the count synced.",
	}, wrapHandler(s, ToolTemplatesReload, s.handleTemplatesReload))
}

// dispatchHandler is the per-tool signature after argument decoding.
// Returning a non-nil error converts to a tool error result; the
// dispatcher publishes the audit envelope based on the error code.
type dispatchHandler[In any] func(ctx context.Context, caller Caller, in In) (any, error)

// wrapHandler builds a ToolHandlerFor that enforces capability, calls
// the typed handler under panic recovery, and emits the audit envelope.
// The dispatcher is the single place tool error semantics are translated
// to MCP wire shapes — handlers return toolError values, never
// CallToolResult.
//
// Free function (rather than a method on *Server) because Go does not
// allow methods to declare additional type parameters; the server is
// captured via the closure on s.
func wrapHandler[In any](s *Server, tool string, fn dispatchHandler[In]) mcp.ToolHandlerFor[In, any] {
	requiredCap := CapForTool(tool)
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		caller := s.callerFromRequest(req)
		callerCtx := withCaller(ctx, caller)
		start := time.Now()

		// Cap check.
		if !caller.HasCap(requiredCap) {
			err := toolError{
				Code:    sextantproto.ErrCodeCapabilityDenied,
				Message: fmt.Sprintf("missing capability %q for tool %q", requiredCap, tool),
				Details: map[string]any{"capability_required": requiredCap, "tool": tool},
			}
			s.audit.publish(auditEvent{
				Tool:       tool,
				Capability: requiredCap,
				Caller:     caller,
				Result:     sextantproto.AuditDenied,
				ErrorCode:  err.Code,
				DurationMs: time.Since(start).Milliseconds(),
			})
			return toolErrorResult(err), nil, nil
		}

		// Dispatch under panic recovery so a runaway tool handler can't
		// crash the MCP server goroutine (which would take the daemon
		// down via mcp.Server.Run).
		out, err := runToolHandler(s, tool, callerCtx, caller, in, fn)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			var te toolError
			if !errors.As(err, &te) {
				te = toolError{Code: sextantproto.ErrCodeInternal, Message: err.Error()}
			}
			s.audit.publish(auditEvent{
				Tool:       tool,
				Capability: requiredCap,
				Caller:     caller,
				Result:     auditResultFor(te.Code),
				ErrorCode:  te.Code,
				DurationMs: dur,
			})
			return toolErrorResult(te), nil, nil
		}

		s.audit.publish(auditEvent{
			Tool:       tool,
			Capability: requiredCap,
			Caller:     caller,
			Result:     sextantproto.AuditAllowed,
			DurationMs: dur,
		})
		return nil, out, nil
	}
}

// runToolHandler invokes fn under a deferred recover so a handler panic
// becomes a clean toolError{Code: internal, Details:{panic}} response
// rather than killing the MCP server goroutine. The stack trace is
// logged so the panic is still investigable.
//
// Symmetric to pkg/rpc/server.go's runHandler — same pattern, separate
// codebase. Both servers must keep this guarantee: one bad handler
// must not take the dispatcher down.
func runToolHandler[In any](
	s *Server,
	tool string,
	ctx context.Context,
	caller Caller,
	in In,
	fn dispatchHandler[In],
) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Printf("mcpserver: tool %q panic from caller %s (%s):\n%v\n%s",
				tool, caller.ID(), caller.Kind, r, debug.Stack())
			out = nil
			err = toolError{
				Code:    sextantproto.ErrCodeInternal,
				Message: fmt.Sprintf("tool %q panicked: %v", tool, r),
				Details: map[string]any{
					"panic": fmt.Sprintf("%v", r),
					"tool":  tool,
				},
			}
		}
	}()
	return fn(ctx, caller, in)
}

// callerFromRequest builds the Caller for a tool invocation. HTTP
// requests carry TokenInfo via Extra; stdio requests don't and default
// to the operator caller.
func (s *Server) callerFromRequest(req *mcp.CallToolRequest) Caller {
	if req == nil || req.Extra == nil {
		return Caller{Kind: CallerOperator}
	}
	return callerFromTokenInfo(req.Extra.TokenInfo)
}

// toolErrorResult builds a CallToolResult that surfaces te to the
// client. The MCP wire pattern (per SDK docs) is to set IsError + put a
// machine-readable JSON blob in TextContent so an SDK that wraps tool
// errors as exceptions still has the structured code accessible.
func toolErrorResult(te toolError) *mcp.CallToolResult {
	body := map[string]any{
		"code":    te.Code,
		"message": te.Message,
	}
	if len(te.Details) > 0 {
		body["details"] = te.Details
	}
	raw, _ := json.Marshal(body)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: body,
		IsError:           true,
	}
}

// ---------------------------------------------------------------------------
// Tool handlers.
// ---------------------------------------------------------------------------

func (s *Server) handleSendMessage(_ context.Context, caller Caller, in SendMessageArgs) (any, error) {
	if in.ToAgent == "" {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "to_agent is required")
	}
	if in.Content == "" {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "content is required")
	}
	subject := "agents." + in.ToAgent + ".inbox"
	payload := map[string]any{
		"from":    caller.ID(),
		"content": in.Content,
	}
	if err := s.publishEnvelope(caller, subject, sextantproto.KindAgentFrame, payload); err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "publish: %v", err)
	}
	return SendMessageResult{OK: true, Subject: subject}, nil
}

func (s *Server) handleBroadcast(_ context.Context, caller Caller, in BroadcastArgs) (any, error) {
	if in.Subject == "" {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "subject is required")
	}
	if in.Content == "" {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "content is required")
	}
	subject := "broadcast." + in.Subject
	payload := map[string]any{
		"from":    caller.ID(),
		"content": in.Content,
	}
	if err := s.publishEnvelope(caller, subject, sextantproto.KindAgentFrame, payload); err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "publish: %v", err)
	}
	return BroadcastResult{OK: true, Subject: subject}, nil
}

func (s *Server) handleEmitEvent(_ context.Context, caller Caller, in EmitEventArgs) (any, error) {
	if in.Subject == "" {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "subject is required")
	}
	subject := "sextant.system." + in.Subject
	payload := map[string]any{
		"from":    caller.ID(),
		"payload": in.Payload,
	}
	if err := s.publishEnvelope(caller, subject, sextantproto.KindAgentFrame, payload); err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "publish: %v", err)
	}
	return EmitEventResult{OK: true, Subject: subject}, nil
}

func (s *Server) handleListAgents(ctx context.Context, _ Caller, in ListAgentsArgs) (any, error) {
	if s.cfg.AgentKV == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "agent KV unavailable")
	}
	// Build a synthetic envelope to reuse the RPC handler.
	args := sextantproto.ListAgentsRequest{}
	if in.Lifecycle != "" {
		args.Filter = &sextantproto.ListAgentsFilter{Lifecycle: in.Lifecycle}
	}
	raw, _ := json.Marshal(args)
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewListAgents(s.cfg.AgentKV), env)
}

func (s *Server) handleAgentStatus(ctx context.Context, _ Caller, in AgentStatusArgs) (any, error) {
	if s.cfg.AgentKV == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "agent KV unavailable")
	}
	if in.AgentID == "" {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "agent_id is required")
	}
	// Build the typed request and use the RPC handler. We marshal into
	// the same JSON shape the handler expects.
	raw, err := json.Marshal(map[string]any{"agent_id": in.AgentID})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal request: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewGetAgentStatus(s.cfg.AgentKV), env)
}

func (s *Server) handleQueryAudit(ctx context.Context, _ Caller, in QueryAuditArgs) (any, error) {
	if s.cfg.QueryDB == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "history backend unavailable")
	}
	// M12 routes the MCP tool through the dedicated query_audit handler
	// so the column shape matches the ClickHouse audit table directly.
	// Previously the tool reused query_history with kind=audit, which
	// projected envelopes from the events table — same data, but a
	// shape mismatch with the spec.
	req := sextantproto.QueryAuditRequest{
		Filter: sextantproto.QueryAuditFilter{
			Actor:  in.Actor,
			Action: in.Action,
		},
		Limit: in.Limit,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal request: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewQueryAudit(s.cfg.QueryDB), env)
}

func (s *Server) handleNotImplemented(_ context.Context, _ Caller, _ any) (any, error) {
	return nil, fmtErrf(sextantproto.ErrCodeNotImplemented, "tool ships in M11 when spawn flow lands")
}

// spawnDepsSnapshot grabs the current SpawnDeps under the mutex so a
// late SetSpawnDeps cannot race with an in-flight tool call.
func (s *Server) spawnDepsSnapshot() *SpawnDeps {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.SpawnDeps
}

// handleSpawnAgent re-uses the RPC spawn handler so the wire shapes
// (envelope + RPCResponse) match the operator NATS path byte-for-byte.
// Returns the raw result map for the MCP client.
func (s *Server) handleSpawnAgent(ctx context.Context, _ Caller, in SpawnAgentArgs) (any, error) {
	deps := s.spawnDepsSnapshot()
	if deps == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "spawn backend not configured")
	}
	raw, err := json.Marshal(sextantproto.SpawnAgentRequest{
		Name:     in.Name,
		Template: in.Template,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewSpawnAgent(handlers.SpawnDeps{
		Definitions:   deps.Definitions,
		Incarnations:  deps.Incarnations,
		Templates:     deps.Templates,
		Containers:    deps.Containers,
		CA:            deps.CA,
		History:       deps.History,
		WorkspaceRoot: deps.WorkspaceDir,
		Worktree:      deps.Worktree,
		RepoRoot:      deps.RepoRoot,
		HostID:        deps.HostID,
		NATSURL:       deps.NATSURL,
		NATSUser:      deps.NATSUser,
		NATSPassword:  deps.NATSPassword,
		MCPURL:        deps.MCPURL,
		Issuer:        deps.Issuer,
		TestRunLabel:  deps.TestRunLabel,
	}), env)
}

func (s *Server) handleKillAgent(ctx context.Context, _ Caller, in KillAgentArgs) (any, error) {
	deps := s.spawnDepsSnapshot()
	if deps == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "spawn backend not configured")
	}
	id, err := uuidFromString(in.AgentID)
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "agent_id: %v", err)
	}
	raw, err := json.Marshal(sextantproto.KillAgentRequest{
		AgentID:      id,
		GraceSeconds: in.GraceSeconds,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  deps.Definitions,
		Incarnations: deps.Incarnations,
		Containers:   deps.Containers,
	}), env)
}

func (s *Server) handleArchiveAgent(ctx context.Context, _ Caller, in ArchiveAgentArgs) (any, error) {
	deps := s.spawnDepsSnapshot()
	if deps == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "spawn backend not configured")
	}
	id, err := uuidFromString(in.AgentID)
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "agent_id: %v", err)
	}
	raw, err := json.Marshal(sextantproto.ArchiveAgentRequest{
		AgentID:      id,
		GraceSeconds: in.GraceSeconds,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  deps.Definitions,
		Incarnations: deps.Incarnations,
		Containers:   deps.Containers,
	}), env)
}

func (s *Server) handlePromptAgent(ctx context.Context, _ Caller, in PromptAgentArgs) (any, error) {
	deps := s.spawnDepsSnapshot()
	if deps == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "spawn backend not configured")
	}
	id, err := uuidFromString(in.AgentID)
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeBadRequest, "agent_id: %v", err)
	}
	raw, err := json.Marshal(sextantproto.PromptAgentRequest{
		AgentID: id,
		Content: in.Content,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions: deps.Definitions,
		NATS:        s.cfg.NATS,
		From:        s.cfg.From,
	}), env)
}

// worktreeMgr returns the configured worktree manager under the mutex
// so a late SetWorktree won't race with an in-flight tool call.
func (s *Server) worktreeMgr() handlers.WorktreeManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Worktree
}

// SetWorktree installs (or replaces) the worktree manager. Safe to
// call after Start: tool handlers re-read the field per invocation.
func (s *Server) SetWorktree(mgr handlers.WorktreeManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Worktree = mgr
}

func (s *Server) handleWorktreeCreate(ctx context.Context, _ Caller, in WorktreeCreateArgs) (any, error) {
	mgr := s.worktreeMgr()
	if mgr == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "worktree manager not configured")
	}
	raw, err := json.Marshal(sextantproto.WorktreeCreateRequest{
		Name:       in.Name,
		BaseBranch: in.BaseBranch,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewWorktreeCreate(handlers.WorktreeDeps{Manager: mgr}), env)
}

func (s *Server) handleWorktreeDestroy(ctx context.Context, _ Caller, in WorktreeDestroyArgs) (any, error) {
	mgr := s.worktreeMgr()
	if mgr == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "worktree manager not configured")
	}
	raw, err := json.Marshal(sextantproto.WorktreeDestroyRequest{
		Name:  in.Name,
		Force: in.Force,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewWorktreeDestroy(handlers.WorktreeDeps{Manager: mgr}), env)
}

func (s *Server) handleWorktreeList(ctx context.Context, _ Caller, _ WorktreeListArgs) (any, error) {
	mgr := s.worktreeMgr()
	if mgr == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "worktree manager not configured")
	}
	raw, _ := json.Marshal(sextantproto.WorktreeListRequest{})
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewWorktreeList(handlers.WorktreeDeps{Manager: mgr}), env)
}

func (s *Server) handleWorktreeMerge(ctx context.Context, _ Caller, in WorktreeMergeArgs) (any, error) {
	mgr := s.worktreeMgr()
	if mgr == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "worktree manager not configured")
	}
	raw, err := json.Marshal(sextantproto.WorktreeMergeRequest{
		Name:   in.Name,
		Target: in.Target,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewWorktreeMerge(handlers.WorktreeDeps{Manager: mgr}), env)
}

func (s *Server) handleWorktreeDiff(ctx context.Context, _ Caller, in WorktreeDiffArgs) (any, error) {
	mgr := s.worktreeMgr()
	if mgr == nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "worktree manager not configured")
	}
	raw, err := json.Marshal(sextantproto.WorktreeDiffRequest{
		Name:    in.Name,
		Against: in.Against,
	})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, s.cfg.From, raw)
	return runRPCAsTool(ctx, handlers.NewWorktreeDiff(handlers.WorktreeDeps{Manager: mgr}), env)
}

// handleTemplatesReload forwards the request to the daemon's
// sextant.control.templates_reload subject. We round-trip through NATS
// rather than calling templates.SyncDirToKV directly so the MCP path
// and the CLI path share one canonical reload site — preserving
// "single-source semantics" for the audit/log lines SyncDirToKV
// produces.
func (s *Server) handleTemplatesReload(ctx context.Context, _ Caller, _ TemplatesReloadArgs) (any, error) {
	reqRaw, err := json.Marshal(sextantd.TemplatesReloadRequest{})
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "marshal: %v", err)
	}
	msg, err := s.cfg.NATS.RequestWithContext(ctx, sextantd.ControlTemplatesReloadSubject, reqRaw)
	if err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "templates_reload request: %v", err)
	}
	var resp sextantd.TemplatesReloadResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "decode response: %v", err)
	}
	if resp.Error != "" {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "%s", resp.Error)
	}
	return TemplatesReloadResult{Count: resp.Count}, nil
}

// runRPCAsTool runs an rpc.Handler synchronously and unmarshals its
// terminal emit into a generic map[string]any so the MCP layer can
// re-marshal it into structured tool output. Errors from the handler
// become toolError values.
func runRPCAsTool(ctx context.Context, h rpc.Handler, env sextantproto.Envelope) (any, error) {
	var (
		resp sextantproto.RPCResponse
		got  bool
	)
	emit := func(r sextantproto.RPCResponse) {
		if !got {
			resp = r
			got = r.Terminal
		}
	}
	if err := h(ctx, env, emit); err != nil {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "%v", err)
	}
	if !got {
		return nil, fmtErrf(sextantproto.ErrCodeInternal, "handler did not emit a terminal reply")
	}
	if resp.Error != nil {
		return nil, toolError{Code: resp.Error.Code, Message: resp.Error.Message, Details: resp.Error.Details}
	}
	var out map[string]any
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return nil, fmtErrf(sextantproto.ErrCodeInternal, "unmarshal result: %v", err)
		}
	} else {
		out = map[string]any{}
	}
	return out, nil
}

// uuidFromString parses s into a uuid.UUID, returning a structured
// bad_request error compatible with the dispatcher when s is malformed.
func uuidFromString(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// publishEnvelope wraps payload in a sextantproto.Envelope tagged with
// the daemon's address (the MCP server is the actor on the bus; the
// caller is recorded in the payload's "from" field). Returns the raw
// marshal/publish error from NATS.
func (s *Server) publishEnvelope(_ Caller, subject string, kind sextantproto.Kind, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	env := sextantproto.NewEnvelope(kind, s.cfg.From, raw)
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return s.cfg.NATS.Publish(subject, body)
}

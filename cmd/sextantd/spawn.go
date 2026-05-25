package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/containermgr"
	"github.com/love-lena/sextant-initial/pkg/mcpserver"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/templates"
)

// spawnRuntime is the daemon-owned bundle of dependencies that drive
// the agent-spawn flow. Built once at startup, handed to both the RPC
// server (operator path) and the MCP server (agent path) so both
// surfaces share one KV / one container manager / one CA.
//
// The daemon also owns the lifecycle: it tears the containermgr down
// at shutdown after stopping every live incarnation.
type spawnRuntime struct {
	containers   *containermgr.Manager
	defsKV       jetstream.KeyValue
	incsKV       jetstream.KeyValue
	templatesKV  jetstream.KeyValue
	hostID       string
	natsURL      string
	natsUser     string
	natsPassword string
	mcpURL       string
	issuer       string
	workspaceDir string
	// testRunLabel, when non-empty, stamps sextant.test_run=<value> on
	// every spawned container so the orphan-tripwire test can scope
	// its check to containers this daemon instance created. Sourced
	// from the SEXTANT_TEST_RUN_LABEL env var at daemon start; empty
	// in production.
	testRunLabel string

	// worktree, when non-nil, is the M14 provider passed through to
	// handlers.SpawnDeps.Worktree. The daemon sets this after the
	// worktree runtime is built; nil when worktree.repo_root is unset.
	worktree handlers.WorktreeProvider
	// repoRoot mirrors worktree.repo_root from the config. The spawn
	// handler uses it to bind-mount <repoRoot>/.git into worktree
	// containers so the worktree's `.git` pointer resolves inside the
	// container. Empty when worktree is disabled.
	repoRoot string
}

// buildSpawnRuntime opens the KV buckets, the docker client, and
// loads the operator credentials so the daemon can hand a fully-wired
// SpawnDeps to its server constructors.
//
// agentDefsKV is reused (RPC list_agents reads from the same handle).
// We re-resolve the other buckets here rather than threading them in so
// the wiring stays self-contained.
func (d *daemon) buildSpawnRuntime(ctx context.Context, nc *nats.Conn, agentDefsKV jetstream.KeyValue) (*spawnRuntime, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	incsKV, err := js.KeyValue(ctx, handlers.AgentIncarnationsBucket)
	if err != nil {
		return nil, fmt.Errorf("open kv %s: %w", handlers.AgentIncarnationsBucket, err)
	}
	tplKV, err := js.KeyValue(ctx, templates.Bucket)
	if err != nil {
		return nil, fmt.Errorf("open kv %s: %w", templates.Bucket, err)
	}

	// Sync templates from ~/.config/sextant/templates/ into KV. M11+
	// startup step per specs/components/sextantd.md §"Startup sequence".
	synced, err := templates.SyncDirToKV(ctx, tplKV, d.cfg.Paths.TemplatesDir)
	if err != nil {
		return nil, fmt.Errorf("sync templates from %s: %w", d.cfg.Paths.TemplatesDir, err)
	}
	log.Printf("sextantd: synced %d template(s) from %s into KV", len(synced), d.cfg.Paths.TemplatesDir)

	// containermgr.New issues a Ping under a short timeout it manages
	// internally; the parent ctx is irrelevant to it.
	mgr, err := containermgr.New(containermgr.Config{}) //nolint:contextcheck // see comment
	if err != nil {
		return nil, fmt.Errorf("containermgr: %w", err)
	}

	creds, err := sextantd.ReadOperatorCreds(d.cfg.NATS.OperatorCreds)
	if err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("read operator creds: %w", err)
	}

	hostID, err := os.Hostname()
	if err != nil || hostID == "" {
		hostID = "local"
	}

	natsSrv := d.currentNATS()
	if natsSrv == nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("no live NATS")
	}
	// Sidecars reach NATS via host.docker.internal on macOS (OrbStack /
	// Docker Desktop both resolve it to the loopback host). The port
	// component matches whatever the kernel allocated on first boot.
	natsURL := buildSidecarNATSURL(natsSrv.Address())

	workspaceRoot := filepath.Join(d.cfg.Paths.DataDir, "spawn-workspaces")
	if err := os.MkdirAll(workspaceRoot, 0o750); err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("mkdir workspace root: %w", err)
	}

	return &spawnRuntime{
		containers:   mgr,
		defsKV:       agentDefsKV,
		incsKV:       incsKV,
		templatesKV:  tplKV,
		hostID:       hostID,
		natsURL:      natsURL,
		natsUser:     creds.User,
		natsPassword: creds.Password,
		// mcpURL is populated by setMCPURL after the MCP server binds.
		mcpURL:       "",
		issuer:       "sextantd@" + hostID,
		workspaceDir: workspaceRoot,
		testRunLabel: os.Getenv("SEXTANT_TEST_RUN_LABEL"),
	}, nil
}

// setMCPURL is called after the MCP HTTP listener binds. We separate
// it from the constructor so the spawnRuntime can be built once and
// then patched with the resolved URL — both surfaces (RPC + MCP) read
// the same pointer.
func (r *spawnRuntime) setMCPURL(addr string) {
	r.mcpURL = sidecarMCPURL(addr)
}

// setWorktree installs the M14 worktree provider and the host repo
// root the spawn handler uses to bind-mount <repoRoot>/.git into
// worktree containers. Called by the daemon after the worktree
// runtime is built (see worktree.go); a nil provider + empty root is
// allowed when the worktree surface is disabled.
func (r *spawnRuntime) setWorktree(p handlers.WorktreeProvider, repoRoot string) {
	r.worktree = p
	r.repoRoot = repoRoot
}

// asSpawnDeps adapts the runtime into the dep bag the handlers expect.
// chConn is the ClickHouse driver.Conn we attach as the history
// writer; nil disables the history-row insert path (handler logs and
// keeps going, but the spawn succeeds).
func (r *spawnRuntime) asSpawnDeps(chConn driver.Conn) handlers.SpawnDeps {
	var hist handlers.HistoryWriter
	if chConn != nil {
		hist = chHistoryWriter{conn: chConn}
	}
	return handlers.SpawnDeps{
		Definitions:   kvMutableAdapter{kv: r.defsKV},
		Incarnations:  kvMutableAdapter{kv: r.incsKV},
		Templates:     r.templatesKV,
		Containers:    r.containers,
		Volumes:       r.containers, // *containermgr.Manager satisfies VolumeManager too
		CA:            nil,          // populated by callers (RPC fills it from d.ca; same handle).
		History:       hist,
		WorkspaceRoot: r.workspaceDir,
		Worktree:      r.worktree,
		RepoRoot:      r.repoRoot,
		HostID:        r.hostID,
		NATSURL:       r.natsURL,
		NATSUser:      r.natsUser,
		NATSPassword:  r.natsPassword,
		MCPURL:        r.mcpURL,
		Issuer:        r.issuer,
		TestRunLabel:  r.testRunLabel,
	}
}

// asMCPDeps adapts the spawn runtime into the mcpserver.SpawnDeps
// shape. The MCP server takes a pointer so the daemon can install it
// after MCP has bound its HTTP listener.
func (r *spawnRuntime) asMCPDeps(ca *authjwt.CA, chConn driver.Conn) *mcpserver.SpawnDeps {
	var hist handlers.HistoryWriter
	if chConn != nil {
		hist = chHistoryWriter{conn: chConn}
	}
	return &mcpserver.SpawnDeps{
		Definitions:  kvMutableAdapter{kv: r.defsKV},
		Incarnations: kvMutableAdapter{kv: r.incsKV},
		Templates:    r.templatesKV,
		Containers:   r.containers,
		Volumes:      r.containers, // *containermgr.Manager also satisfies VolumeManager
		CA:           ca,
		History:      hist,
		WorkspaceDir: r.workspaceDir,
		Worktree:     r.worktree,
		RepoRoot:     r.repoRoot,
		HostID:       r.hostID,
		NATSURL:      r.natsURL,
		NATSUser:     r.natsUser,
		NATSPassword: r.natsPassword,
		MCPURL:       r.mcpURL,
		Issuer:       r.issuer,
		TestRunLabel: r.testRunLabel,
	}
}

// kvMutableAdapter wraps a jetstream.KeyValue so it satisfies
// handlers.AgentMutableKV. The real type has the full surface; the
// adapter is a tiny indirection so the handler's interface stays
// narrow.
type kvMutableAdapter struct {
	kv jetstream.KeyValue
}

func (a kvMutableAdapter) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	return a.kv.Get(ctx, key)
}

func (a kvMutableAdapter) ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	return a.kv.ListKeys(ctx, opts...)
}

func (a kvMutableAdapter) Put(ctx context.Context, key string, value []byte) (uint64, error) {
	return a.kv.Put(ctx, key, value)
}

func (a kvMutableAdapter) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	return a.kv.Delete(ctx, key, opts...)
}

// chHistoryWriter adapts driver.Conn to the narrow HistoryWriter
// interface the spawn handler accepts. Exec swallows the affected-row
// count — we don't need it.
type chHistoryWriter struct {
	conn driver.Conn
}

func (w chHistoryWriter) Exec(ctx context.Context, query string, args ...any) error {
	return w.conn.Exec(ctx, query, args...)
}

// buildSidecarNATSURL turns the NATS server's bound address into a URL
// the sidecar inside a container can reach. On macOS with OrbStack /
// Docker Desktop, the host is reachable at host.docker.internal; on
// Linux the same alias resolves through the docker-host bridge.
func buildSidecarNATSURL(addr string) string {
	// addr is host:port; we keep only the port and substitute host.
	host, port := splitAddr(addr)
	if host == "127.0.0.1" || host == "0.0.0.0" || host == "localhost" {
		host = "host.docker.internal"
	}
	if port == "" {
		port = "4222"
	}
	return "nats://" + host + ":" + port
}

// sidecarMCPURL is the matching transform for the MCP HTTP listener.
func sidecarMCPURL(addr string) string {
	host, port := splitAddr(addr)
	if host == "127.0.0.1" || host == "0.0.0.0" || host == "localhost" || host == "" {
		host = "host.docker.internal"
	}
	if port == "" {
		port = "5172"
	}
	return "http://" + host + ":" + port + "/mcp"
}

// splitAddr extracts host + port from "host:port" with no allocation
// when the input is well-formed. Returns the input unchanged in the
// host slot if it has no colon.
func splitAddr(addr string) (string, string) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:]
		}
	}
	return addr, ""
}

// stopRunningIncarnations is the M11 shutdown hook: walk the
// agent_incarnations KV, find every entry whose State is still
// "starting" or "ready" (i.e. EndedAt is nil), and stop the matching
// container with the per-template grace. Best-effort — a stop failure
// is logged but does not abort the rest of the shutdown.
//
// Called by the daemon's doShutdown before NATS / ClickHouse go down,
// so the sidecar's last heartbeat doesn't race the bus tear-down.
func (d *daemon) stopRunningIncarnations(ctx context.Context) {
	d.mu.Lock()
	rt := d.spawnRT
	d.mu.Unlock()
	if rt == nil || rt.containers == nil || rt.incsKV == nil {
		return
	}
	lister, err := rt.incsKV.ListKeys(ctx)
	if err != nil {
		// Empty bucket or NATS already gone; log and return.
		if errors.Is(err, jetstream.ErrNoKeysFound) || errors.Is(err, jetstream.ErrKeyNotFound) {
			return
		}
		log.Printf("sextantd: shutdown: list incarnations: %v", err)
		return
	}
	defer func() { _ = lister.Stop() }()
	for key := range lister.Keys() {
		entry, err := rt.incsKV.Get(ctx, key)
		if err != nil {
			continue
		}
		var inc sextantproto.AgentIncarnation
		if err := json.Unmarshal(entry.Value(), &inc); err != nil {
			continue
		}
		if inc.EndedAt != nil || inc.State == sextantproto.IncarnationExited || inc.State == sextantproto.IncarnationFailed {
			continue
		}
		if inc.ContainerID == "" {
			continue
		}
		log.Printf("sextantd: shutdown: stopping incarnation %s (container=%s)", inc.IncarnationID, inc.ContainerID)
		stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := rt.containers.Stop(stopCtx, inc.ContainerID, 10*time.Second); err != nil {
			log.Printf("sextantd: shutdown: stop %s: %v", inc.ContainerID, err)
		}
		cancel()
		// Mark the incarnation as exited so the next boot sees it
		// closed out.
		now := sextantproto.AtTimestamp(time.Now().UTC())
		inc.State = sextantproto.IncarnationExited
		inc.EndedAt = &now
		raw, err := json.Marshal(inc)
		if err == nil {
			_, _ = rt.incsKV.Put(ctx, key, raw)
		}
	}
}

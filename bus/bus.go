// Package bus runs an embedded Sextant bus: a NATS server (JetStream) with the
// reserved sx namespace bootstrapped, authenticated with decentralized JWT auth
// so every client connects as its own verified identity (ADR-0007, ADR-0012).
//
// Auth model (see auth.go): one operator, one SEXTANT account holding all
// clients and the sx_ infra, and one user JWT per client. All clients share the
// client-tier permission guardrail for now; per-client (write-precision)
// permissions are the deferred refinement. Two NATS realities still shape the
// guardrail, both flagged in review:
//   - whole-token subject wildcards mean "KV_sx_*" is inexpressible, so v1
//     denies clients ALL bucket/stream lifecycle (the operator provisions
//     buckets) — a superset of "no sx_ bucket squatting";
//   - deny-wins means arbitrary transient publishes to sx.* (other than
//     sx.control.*) are not yet denied.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/love-lena/sextant/bus/internal/backend"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/protocol/wire"
	"github.com/love-lena/sextant/protocol/wireapi"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Config configures an embedded bus.
type Config struct {
	// StoreDir is the JetStream storage + key material directory. Required.
	StoreDir string
	// Port is the listen port; 0 or -1 picks a random available port.
	Port int
	// Logf is the library's only output channel: every diagnostic the bus
	// emits (the port-fallback notice, a dropped undecodable frame, …) goes
	// through it, one Printf-style call per line, with no trailing newline in
	// format. It may be called from concurrent goroutines. Nil means the
	// default: write the line to stderr — exactly what a zero-config embedder
	// sees today.
	Logf func(format string, args ...any)

	// HeartbeatFreshness is the heartbeat freshness window (TASK-126): a client
	// whose most recent clients.heartbeat is within it is derived online even when
	// the connection table cannot see it (the leaf case). Zero means the default
	// (defaultHeartbeatFreshness). It is the bus's tolerance, independent of the
	// SDK's beat interval, and should be a small multiple of it.
	HeartbeatFreshness time.Duration

	// LeafListenAddr, when set, opens a leaf-node listener on the hub so a remote
	// leaf can link in (ADR-0038). It is a host:port (e.g. "127.0.0.1:7422" behind
	// a secure transport). Empty means no leaf listener — the default, no behavior
	// change. The link MUST ride a secure transport (SSH-R / Tailscale / WireGuard);
	// native leaf-listener TLS is a follow-up. Mutually exclusive with leaf-remote.
	LeafListenAddr string

	// WebSocketListenAddr, when set, opens a loopback WebSocket listener on the bus
	// so a browser dash can connect as a co-equal TS client over ws (ADR-0044). It
	// is a loopback host:port (e.g. "127.0.0.1:7423"). Empty means no WebSocket
	// listener — the default, no behavior change. Like the leaf listener it is
	// loopback-only and NoTLS, sitting behind the operator's secure transport;
	// native wss TLS is a follow-up.
	WebSocketListenAddr string

	// BrowserCredTTL bounds the lifetime of a mint-on-behalf credential issued for
	// a kind=="browser" child (ADR-0044): the dash mints a short-lived browser
	// credential it cannot retire, so the JWT exp is the cleanup. Zero means the
	// default (defaultBrowserCredTTL); every non-browser mint is unaffected
	// (perpetual, ttl=0). Overridable so the operator can widen it for a long-open
	// tab or narrow it for a stricter posture.
	BrowserCredTTL time.Duration

	// LeafRemoteURL, when set, runs this bus in LEAF mode (ADR-0038): instead of an
	// authoritative hub, it links to a remote hub at this nats-leaf:// URL,
	// federates the per-client wire-API subjects to it, and keeps JetStream OFF (the
	// engine stays at the hub). LeafBundle and LeafCreds are required with it. Empty
	// means hub mode — the default. Mutually exclusive with LeafListenAddr.
	LeafRemoteURL string

	// LeafCreds is the path to the SEXTANT-user link credential the hub minted for
	// this leaf (ADR-0038). Required in leaf mode; the leaf authenticates the link
	// to the hub with it.
	LeafCreds string

	// LeafBundle is the path to the hub's public trust bundle (operator + SEXTANT +
	// system account JWTs and the SEXTANT account public key — PUBLIC claims only,
	// no signing seeds). Required in leaf mode: the leaf installs these so it trusts
	// the hub's operator and enforces the same per-client perms locally, yet holds
	// no seed and so CANNOT mint — minting stays at the hub (ADR-0038, the trust
	// model's key-custody half).
	LeafBundle string
}

// logf returns the resolved log function: cfg.Logf, or the stderr default.
func (cfg Config) logf() func(format string, args ...any) {
	if cfg.Logf != nil {
		return cfg.Logf
	}
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

// Bus is a running embedded NATS server with the sx namespace bootstrapped. It
// also serves the protocol's operations as calls over the Wire API (serve.go).
type Bus struct {
	ns     *natsserver.Server
	opConn *nats.Conn
	url    string
	store  string
	ident  *identity // signing material — the bus is the sole minter (ADR-0020)

	// Operation serving (ADR-0018/0019): the backend the operations run against,
	// the Wire API subscription, and the bounded worker semaphore.
	backend backend.Backend
	apiSub  *nats.Subscription
	apiSem  chan struct{}

	// Push-stream relays (ADR-0019 subscribe/watch): a per-(clientID, subID)
	// registry of running relays from the backend into sx.deliver.<id>.<subID>.
	// All relay goroutines are rooted at relayCtx, so stopServing cancels them en
	// masse; a single relay is cancelled (and removed) on an explicit
	// subscription.stop. Crash-driven teardown (a client that never stops) is
	// TASK-20 liveness, the same gap the clients registry has.
	relayCtx    context.Context
	relayCancel context.CancelFunc
	relaysMu    sync.Mutex
	relays      map[string]map[string]*relay

	// freshnessWindow is how recently a client must have heartbeated to be
	// derived online when the connection table does not show it (TASK-126). It is
	// the OR-half of the dual-source presence rule: online = Connz-online OR
	// last_seen within this window. Resolved from Config.HeartbeatFreshness (or
	// defaultHeartbeatFreshness) at Start.
	freshnessWindow time.Duration

	// browserCredTTL bounds the JWT lifetime of a mint-on-behalf credential whose
	// child kind is "browser" (ADR-0044): the dash mints a short-lived browser
	// credential it cannot retire, so the exp is the cleanup. Resolved from
	// Config.BrowserCredTTL (or defaultBrowserCredTTL) at Start. Every other mint
	// is perpetual (ttl=0), unchanged.
	browserCredTTL time.Duration

	// hbAfterReadHook is a test-only seam (set via SetHeartbeatAfterReadHook):
	// opClientsHeartbeat calls it, when non-nil, between reading the registry
	// record and writing last_seen back, so a test can force a concurrent
	// retire-delete into that window. Always nil in production.
	hbAfterReadHook func()

	// logf is the resolved Config.Logf (never nil): the bus's only output
	// channel. Components log through it instead of writing to stderr.
	logf func(format string, args ...any)
}

// defaultHeartbeatFreshness is the bus's default heartbeat freshness window: a
// client whose last beat is within it is derived online even when the connection
// table cannot see it (the leaf case, TASK-126). It is generously wider than the
// SDK's default heartbeat interval (~15s) so an occasional missed beat does not
// flap presence — roughly the SDK's own freshness multiple.
const defaultHeartbeatFreshness = 45 * time.Second

// defaultBrowserCredTTL is the lifetime a mint-on-behalf credential gets when the
// child is kind=="browser" (ADR-0044): the dash mints a short-lived browser
// credential it cannot retire, so the JWT exp is the cleanup. One hour balances
// a long-open tab against a stale credential lingering after a tab closes;
// overridable via Config.BrowserCredTTL. Every non-browser mint stays perpetual
// (ttl=0), unchanged.
const defaultBrowserCredTTL = time.Hour

// stablePort resolves the listen port for a (re)start. If cfg.Port is non-zero
// the caller asked for a specific port: it is a deterministic pin — probe it and
// FAIL LOUD when it is unavailable (a non-nil err), never silently fall back, so
// an operator who pinned a port either gets that port or a clear reason why not
// (the v0.5.1 outage: a port change must never be silent). Otherwise look for a
// previous address in the store's bus.json: same store ⇒ same address when the
// port is still free (ADR-0025). It returns the port to use (−1 means "let the
// OS pick") and, when a recorded port was found but unavailable, a non-empty
// notice to log loudly before binding the random fallback.
func stablePort(storeDir string, cfgPort int) (port int, notice string, err error) {
	if cfgPort > 0 {
		// Explicit pin (a positive port; 0 and -1 mean "random" per Config.Port).
		// Probe it so a conflict is a clear, port-named failure here rather than an
		// opaque "listener failed to start" later (DontListen defers the real bind).
		// Closing the probe leaves a small TOCTOU window before AcceptLoop re-binds;
		// a loser there does NOT silently come up — AcceptLoop logs and returns
		// without setting a listener, so ns.Addr() is nil and Start fails loud at
		// the open-listener check below (regression-tested: TestExplicitPort*).
		ln, perr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfgPort))
		if perr != nil {
			return 0, "", fmt.Errorf("bus: requested port %d is unavailable: %w (free it or pick another with --port / `sextant config set port`)", cfgPort, perr)
		}
		_ = ln.Close()
		return cfgPort, "", nil
	}
	prev, ok := recordedPort(storeDir)
	if !ok {
		return -1, "", nil // fresh store or unreadable file — ephemeral is correct
	}
	ln, perr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", prev))
	if perr != nil {
		// Port is taken by something else — fall back to random and warn LOUDLY:
		// every client pinned to the old port must re-resolve via bus.json.
		return -1, fmt.Sprintf("bus: recorded port %d unavailable — binding a RANDOM port; clients pinned to %d must re-resolve from bus.json (pin a deterministic port with --port / `sextant config set port` to avoid this)", prev, prev), nil
	}
	// Port is free — release the probe listener and let NATS bind it.
	_ = ln.Close()
	return prev, "", nil
}

// recordedPort reads the bus URL from the store's discovery file and returns
// the port number. Returns (0, false) if the file is absent, unreadable, or
// contains no parseable port.
func recordedPort(storeDir string) (int, bool) {
	info, err := conninfo.Read(filepath.Join(storeDir, conninfo.DefaultFile))
	if err != nil {
		return 0, false
	}
	u, err := url.Parse(info.URL)
	if err != nil {
		return 0, false
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p <= 0 {
		return 0, false
	}
	return p, true
}

// Start launches the embedded bus under JWT auth and bootstraps the reserved
// buckets. The caller must Shutdown it. With Config.LeafRemoteURL set it starts a
// LEAF instead (ADR-0038): a JetStream-off bus that links to a remote hub and
// federates the per-client wire-API subjects — see startLeaf.
func Start(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.StoreDir == "" {
		return nil, errors.New("bus: StoreDir is required")
	}
	if err := validateLeafConfig(cfg); err != nil {
		return nil, err
	}
	if cfg.LeafRemoteURL != "" {
		return startLeaf(ctx, cfg)
	}
	ident, err := loadOrCreateIdentity(cfg.StoreDir)
	if err != nil {
		return nil, err
	}
	logf := cfg.logf()
	port, portNotice, err := stablePort(cfg.StoreDir, cfg.Port)
	if err != nil {
		return nil, err
	}
	if portNotice != "" {
		logf("%s", portNotice)
	}

	opts := &natsserver.Options{
		ServerName: "sextant",
		Host:       "127.0.0.1",
		Port:       port, // set by stablePort: previous port, caller-requested, or −1 (ephemeral)
		JetStream:  true,
		StoreDir:   cfg.StoreDir,
		NoSigs:     true, // the CLI owns signal handling
		// Start with the client TCP listener closed. No external client can
		// connect until bootstrap has provisioned the buckets and written the
		// epoch; we open the listener explicitly once that's done (below). This
		// makes "the bus is reachable" and "the epoch is present" atomic from a
		// client's point of view — a client can never connect into a half-ready
		// bus and fail its epoch read.
		DontListen: true,
	}
	if err := ident.serverAuthOptions(opts); err != nil {
		return nil, err
	}
	// Hub leaf listener (ADR-0038): default-off; only when --leaf-listen is set. It
	// lets a remote leaf link into this hub. The listener MUST sit behind a secure
	// transport (the bus does not open a routable unencrypted leaf listener).
	if cfg.LeafListenAddr != "" {
		if err := applyHubLeafListener(opts, cfg.LeafListenAddr); err != nil {
			return nil, err
		}
	}
	// Bus WebSocket listener (ADR-0044): default-off; only when --ws-listen /
	// config websocket-listen is set. It lets a browser dash connect as a co-equal
	// TS client over ws. Loopback-only and NoTLS — like the leaf listener it sits
	// behind the operator's secure transport (loopback / SSH-R / Tailscale); native
	// wss TLS is a follow-up.
	if cfg.WebSocketListenAddr != "" {
		if err := applyWebSocketListener(opts, cfg.WebSocketListenAddr); err != nil {
			return nil, err
		}
	}

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("bus: new server: %w", err)
	}
	ns.Start()
	if err := waitReady(ctx, ns, 10*time.Second); err != nil {
		ns.Shutdown()
		return nil, err
	}

	freshness := cfg.HeartbeatFreshness
	if freshness <= 0 {
		freshness = defaultHeartbeatFreshness
	}
	browserTTL := cfg.BrowserCredTTL
	if browserTTL <= 0 {
		browserTTL = defaultBrowserCredTTL
	}
	b := &Bus{ns: ns, store: cfg.StoreDir, ident: ident, freshnessWindow: freshness, browserCredTTL: browserTTL, logf: logf}

	// The bus's own operator-tier connection is in-process: it needs no TCP
	// listener, so bootstrap runs while the client port is still closed and
	// races nothing. The same connection carries control broadcasts (Drain) for
	// the bus's lifetime.
	opJWT, opSeed, _, err := ident.mintUser("sextant-operator", operatorPermissions(), 0)
	if err != nil {
		ns.Shutdown()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("bus: %w", err)
	}
	opConn, err := nats.Connect("", nats.InProcessServer(ns),
		nats.UserJWTAndSeed(opJWT, opSeed), nats.Name("sextant-operator"))
	if err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("bus: operator connect: %w", err)
	}
	b.opConn = opConn

	if err := b.bootstrap(ctx); err != nil {
		opConn.Close()
		ns.Shutdown()
		return nil, err
	}

	// Start serving the protocol's operations before any client can connect, so a
	// client that connects the instant the listener opens can immediately call.
	if err := b.startServing(); err != nil {
		opConn.Close()
		ns.Shutdown()
		return nil, err
	}

	// Provision the two reserved infrastructure credentials in the store (ADR-0020):
	// the operator credential (held-identity mode: mint for another, retire) and the
	// enrollment credential (bootstrap mode: a local process self-enrolls). The
	// signing keys never leave the bus — these are minted credentials, the locality-
	// trusted way the operator and an identity-less local process reach the issuance
	// path. Done before the listener opens, so they exist the instant a client could.
	if err := b.provisionInfraCreds(); err != nil {
		opConn.Close()
		ns.Shutdown()
		return nil, err
	}

	// Bootstrap is done: open the client TCP listener. AcceptLoop binds the port
	// and spawns the accept goroutine, then returns — so only now can a client
	// connect, and the epoch it reads at connect is already present. If the bind
	// fails (a taken port — including the rare probe-close→bind TOCTOU loser for an
	// explicit port), AcceptLoop logs and returns WITHOUT a listener: Addr() is nil
	// and we fail loud here rather than handing back a bus with no client listener.
	ns.AcceptLoop(make(chan struct{}))
	if ns.Addr() == nil {
		opConn.Close()
		ns.Shutdown()
		return nil, fmt.Errorf("bus: client listener failed to bind port %d (the port was taken after the pre-bind probe; free it or pin another with --port / `sextant config set port`)", port)
	}
	b.url = ns.ClientURL()

	// With a leaf listener open, write the public trust bundle + a hub-minted link
	// credential into the store (ADR-0038), so an operator can carry both to the
	// remote box. Done after the listener is up: the bundle and link are about
	// reaching this hub, which now exists.
	if cfg.LeafListenAddr != "" {
		if err := b.writeLeafArtifacts(); err != nil {
			opConn.Close()
			ns.Shutdown()
			return nil, err
		}
	}
	return b, nil
}

// bootstrap creates the reserved buckets idempotently and publishes the
// protocol epoch, as the operator. The bus is authoritative for its epoch, so
// the write is unconditional — it self-heals if a prior run wrote a different
// value (clients hard-gate on it at connect; see ADR-0010).
func (b *Bus) bootstrap(ctx context.Context) error {
	js, err := jetstream.New(b.opConn)
	if err != nil {
		return fmt.Errorf("bus: jetstream: %w", err)
	}
	for _, spec := range sx.Buckets() {
		if _, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  spec.Name,
			History: spec.History,
			Storage: jetstream.FileStorage,
		}); err != nil {
			return fmt.Errorf("bus: bootstrap bucket %s: %w", spec.Name, err)
		}
	}
	meta, err := js.KeyValue(ctx, sx.BucketMeta)
	if err != nil {
		return fmt.Errorf("bus: open %s: %w", sx.BucketMeta, err)
	}
	if _, err := meta.Put(ctx, sx.MetaKeyEpoch, []byte(strconv.Itoa(wire.Epoch))); err != nil {
		return fmt.Errorf("bus: write protocol epoch: %w", err)
	}

	// Default the principal designation to the operator's seat (ADR-0030). At
	// bootstrap no human client ULID exists yet — the operator self-enrolls their
	// seat AFTER the bus is up — so the seat that exists here is the reserved
	// operator identity (the bus-owner credential tier, ADR-0015/0020). It is the
	// root of authority by construction, which is exactly what the principal is.
	// The operator re-points it to the human's minted client ULID with
	// `sextant principal set <ulid>` once enrolled (the two-way door). Unlike the
	// epoch this is a default, not an authority the bus owns: only write it if
	// absent, so a re-designation survives a bus restart of the same store.
	// (b.backend is not wired until startServing, so this reads the meta KV
	// directly, as the epoch write above does.)
	if _, err := meta.Get(ctx, sx.MetaKeyPrincipal); errors.Is(err, jetstream.ErrKeyNotFound) {
		if _, err := meta.Put(ctx, sx.MetaKeyPrincipal, []byte(wireapi.OperatorID)); err != nil {
			return fmt.Errorf("bus: write principal designation: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("bus: read principal designation: %w", err)
	}

	// The durable Messages stream. Clients can't create streams (guardrail), so
	// the operator provisions it. Retention is a lean 7 days (an open item).
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      sx.StreamMessages,
		Subjects:  []string{sx.MessagePrefix + ">"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Storage:   jetstream.FileStorage,
	}); err != nil {
		return fmt.Errorf("bus: bootstrap messages stream: %w", err)
	}

	// The artifacts bucket (operator-provisioned; clients can't create buckets).
	if _, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  sx.BucketArtifacts,
		History: sx.ArtifactHistory,
		Storage: jetstream.FileStorage,
	}); err != nil {
		return fmt.Errorf("bus: bootstrap artifacts bucket: %w", err)
	}
	return nil
}

// waitReady polls for server readiness, honoring ctx and a hard upper bound.
func waitReady(ctx context.Context, ns *natsserver.Server, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if ns.ReadyForConnections(50 * time.Millisecond) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("bus: waiting for server: %w", err)
		}
		if time.Now().After(deadline) {
			return errors.New("bus: server not ready within timeout")
		}
	}
}

// Drain delivers the cooperative-drain signal to every online client over its
// own push space (sx.deliver.<id>.drain), so a client needs no extra permission
// beyond its delivery subscription to receive it (ADR-0010, ADR-0019). It targets
// the clients that are connected right now — derived from the live connection
// table (ADR-0020), the same source of truth as presence — so there is no
// register/deregister-maintained set to drift out of sync.
func (b *Bus) Drain() error {
	if b.isLeaf() {
		// A leaf holds no operator connection or backend — Drain is a hub act
		// (the hub owns the delivery space and the registry). Return a clean error
		// rather than nil-deref; the CLI's leaf path does not call Drain at all.
		return errors.New("bus: drain is unavailable on a leaf — the hub drains its clients")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ids, err := b.onlineClientIDs(ctx)
	if err != nil {
		return fmt.Errorf("bus: drain: %w", err)
	}
	for _, id := range ids {
		if err := b.opConn.Publish(wireapi.DeliverSubject(id, wireapi.DrainSubID), nil); err != nil {
			return fmt.Errorf("bus: publish drain to %s: %w", id, err)
		}
	}
	return b.opConn.Flush()
}

// Shutdown stops the embedded server and closes the operator connection.
func (b *Bus) Shutdown() {
	b.stopServing()
	if b.opConn != nil {
		b.opConn.Close()
	}
	b.ns.Shutdown()
	b.ns.WaitForShutdown()
}

// ClientURL is the address clients connect to.
func (b *Bus) ClientURL() string { return b.url }

// isLeaf reports whether this bus is running in leaf mode (ADR-0038). A leaf is
// built by startLeaf, which loads no signing identity, opens no operator
// connection, and wires no backend — the engine and the sole minter are the hub.
// ident==nil is the one invariant that holds for every leaf and no hub, so it is
// the test the hub-only operations (mint, drain) guard on to fail clean rather
// than nil-deref.
func (b *Bus) isLeaf() bool { return b.ident == nil }

// MintClient is the issuance path (ADR-0020): the bus mints a NEW client identity
// — a fresh ULID id and its per-client credential (JWT+seed) — AND persists its
// durable registry record, so the identity exists and can connect. The signing
// keys never leave the bus. It returns the credential text and the minted id.
// This is what clients.register calls once it has authorized the caller; tests
// use it directly to issue a client. The kind is the client's self-declared role
// (e.g. "worker"); display_name is its human label.
func (b *Bus) MintClient(ctx context.Context, displayName, kind string) (creds, id string, err error) {
	return b.mintClient(ctx, displayName, kind, "")
}

// mintClient is MintClient with the spawnedBy lineage marker (ADR-0033): empty
// for a top-level identity (operator/enrollment issuance), or the minting client's
// id for a mint-on-behalf child — which both records the spawn lineage and fences
// that child out of dispatching children of its own.
func (b *Bus) mintClient(ctx context.Context, displayName, kind, spawnedBy string) (creds, id string, err error) {
	if b.isLeaf() {
		// A leaf holds no signing identity or backend by construction (ADR-0038) —
		// minting stays at the hub. Return a clean error rather than nil-deref on
		// b.ident / b.backend.
		return "", "", errors.New("bus: minting is unavailable on a leaf — mint at the hub (the leaf holds no signing key)")
	}
	// A browser child (ADR-0044) gets a bounded JWT lifetime: the dash mints it
	// over clients.register but cannot retire it, so the exp is the cleanup. Every
	// other kind is perpetual (ttl=0), unchanged.
	var ttl time.Duration
	if kind == wireapi.KindBrowser {
		ttl = b.browserCredTTL
	}
	creds, id, subject, err := b.mintIdentity(displayName, ttl)
	if err != nil {
		return "", "", err
	}
	epoch, err := b.readEpoch(ctx)
	if err != nil {
		return "", "", fmt.Errorf("bus: issue: %w", err)
	}
	rec, err := json.Marshal(wireapi.ClientEntry{
		ID:          id,
		Subject:     subject,
		DisplayName: displayName,
		Kind:        kind,
		Epoch:       epoch,
		IssuedAt:    nowRFC3339(),
		SpawnedBy:   spawnedBy,
	})
	if err != nil {
		return "", "", fmt.Errorf("bus: issue: encode record: %w", err)
	}
	if _, err := b.backend.Put(ctx, sx.BucketClients, id, rec); err != nil {
		return "", "", fmt.Errorf("bus: issue: persist record: %w", err)
	}
	return creds, id, nil
}

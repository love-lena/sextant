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

	"github.com/love-lena/sextant/internal/backend"
	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
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

	// logf is the resolved Config.Logf (never nil): the bus's only output
	// channel. Components log through it instead of writing to stderr.
	logf func(format string, args ...any)
}

// stablePort resolves the listen port for a (re)start. If cfg.Port is non-zero
// the caller asked for a specific port — use it as-is. Otherwise look for a
// previous address in the store's bus.json: same store ⇒ same address when the
// port is still free (ADR-0025). It returns the port to use (−1 means
// "let the OS pick") and, when a recorded port was found but unavailable, a
// non-empty notice to log.
func stablePort(storeDir string, cfgPort int) (port int, notice string) {
	if cfgPort != 0 {
		return cfgPort, ""
	}
	prev, ok := recordedPort(storeDir)
	if !ok {
		return -1, "" // fresh store or unreadable file — ephemeral is correct
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", prev))
	if err != nil {
		// Port is taken by something else — fall back and warn.
		return -1, fmt.Sprintf("bus: recorded port %d is in use; starting on a new port (enrolled contexts may need updating)", prev)
	}
	// Port is free — release the probe listener and let NATS bind it.
	_ = ln.Close()
	return prev, ""
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
// buckets. The caller must Shutdown it.
func Start(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.StoreDir == "" {
		return nil, errors.New("bus: StoreDir is required")
	}
	ident, err := loadOrCreateIdentity(cfg.StoreDir)
	if err != nil {
		return nil, err
	}
	logf := cfg.logf()
	port, portNotice := stablePort(cfg.StoreDir, cfg.Port)
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

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("bus: new server: %w", err)
	}
	ns.Start()
	if err := waitReady(ctx, ns, 10*time.Second); err != nil {
		ns.Shutdown()
		return nil, err
	}

	b := &Bus{ns: ns, store: cfg.StoreDir, ident: ident, logf: logf}

	// The bus's own operator-tier connection is in-process: it needs no TCP
	// listener, so bootstrap runs while the client port is still closed and
	// races nothing. The same connection carries control broadcasts (Drain) for
	// the bus's lifetime.
	opJWT, opSeed, _, err := ident.mintUser("sextant-operator", operatorPermissions())
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
	// connect, and the epoch it reads at connect is already present.
	ns.AcceptLoop(make(chan struct{}))
	if ns.Addr() == nil {
		opConn.Close()
		ns.Shutdown()
		return nil, errors.New("bus: client listener failed to start")
	}
	b.url = ns.ClientURL()
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
	creds, id, subject, err := b.mintIdentity(displayName)
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

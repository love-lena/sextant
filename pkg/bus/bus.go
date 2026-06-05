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
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/love-lena/sextant/internal/backend"
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
}

// Bus is a running embedded NATS server with the sx namespace bootstrapped. It
// also serves the protocol's operations as calls over the Wire API (serve.go).
type Bus struct {
	ns     *natsserver.Server
	opConn *nats.Conn
	url    string
	store  string

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
	port := cfg.Port
	if port == 0 {
		port = -1 // random available port
	}

	opts := &natsserver.Options{
		ServerName: "sextant",
		Host:       "127.0.0.1",
		Port:       port,
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

	b := &Bus{ns: ns, store: cfg.StoreDir}

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

// Drain broadcasts the cooperative-drain control message (ADR-0010).
func (b *Bus) Drain() error {
	if err := b.opConn.Publish(sx.SubjectDrain, nil); err != nil {
		return fmt.Errorf("bus: publish drain: %w", err)
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

// MintClient mints a client-tier credentials file for a client with the given
// human display_name — the bus mints its primary id (a ULID), with the shared
// client-tier guardrail. It returns the creds file and the minted id.
func (b *Bus) MintClient(displayName string) (creds, id string, err error) {
	return MintClientToken(b.store, displayName)
}

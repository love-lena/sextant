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
	"time"

	"github.com/love-lena/sextant/pkg/sx"
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

// Bus is a running embedded NATS server with the sx namespace bootstrapped.
type Bus struct {
	ns     *natsserver.Server
	opConn *nats.Conn
	url    string
	store  string
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

	b := &Bus{ns: ns, url: ns.ClientURL(), store: cfg.StoreDir}

	// The bus's own operator-tier connection (bootstrap + control broadcasts).
	opJWT, opSeed, err := ident.mintUser("sextant-operator", operatorPermissions())
	if err != nil {
		ns.Shutdown()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("bus: %w", err)
	}
	opConn, err := nats.Connect(b.url, nats.UserJWTAndSeed(opJWT, opSeed), nats.Name("sextant-operator"))
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
	return b, nil
}

// bootstrap creates the reserved buckets idempotently, as the operator.
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
	if b.opConn != nil {
		b.opConn.Close()
	}
	b.ns.Shutdown()
	b.ns.WaitForShutdown()
}

// ClientURL is the address clients connect to.
func (b *Bus) ClientURL() string { return b.url }

// MintClient mints a client-tier credentials file for id — its own verified
// identity, with the shared client-tier guardrail.
func (b *Bus) MintClient(id string) (string, error) {
	return MintClientToken(b.store, id)
}

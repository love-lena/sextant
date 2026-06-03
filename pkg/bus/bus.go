// Package bus runs an embedded Sextant bus: a NATS server (JetStream) with the
// reserved sx namespace bootstrapped and two static credential tiers carrying
// the ADR-0012 guardrail.
//
// The guardrail realizes ADR-0012's coarse, day-one protection. Two notes on
// how it maps onto NATS, both flagged for review:
//   - NATS subject wildcards are whole-token, so "KV_sx_*" cannot be expressed.
//     v1 therefore denies clients ALL stream/bucket lifecycle (the operator
//     provisions buckets) — a superset of "no sx_ bucket squatting".
//   - Re-allowing a subset of a denied wildcard is not possible, so arbitrary
//     transient publishes to sx.* (other than sx.control.*) are not yet denied.
//
// Per-client distinct credentials (write-precision) remain the deferred item
// from ADR-0012; v1 issues two static tiers, operator and client.
package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	operatorUser = "operator"
	clientUser   = "client"
)

// Config configures an embedded bus.
type Config struct {
	// StoreDir is the JetStream storage directory. Required.
	StoreDir string
	// Port is the listen port; 0 or -1 picks a random available port.
	Port int
	// OperatorPassword / ClientPassword override the generated credentials.
	OperatorPassword string
	ClientPassword   string
}

// Bus is a running embedded NATS server with the sx namespace bootstrapped.
type Bus struct {
	ns     *natsserver.Server
	opConn *nats.Conn

	url            string
	opPass, clPass string
}

// Start launches the embedded bus, applies the credential tiers, and
// bootstraps the reserved buckets. The caller must Shutdown it.
func Start(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.StoreDir == "" {
		return nil, errors.New("bus: StoreDir is required")
	}
	opPass := cfg.OperatorPassword
	if opPass == "" {
		opPass = randHex()
	}
	clPass := cfg.ClientPassword
	if clPass == "" {
		clPass = randHex()
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
		Users: []*natsserver.User{
			{Username: operatorUser, Password: opPass}, // nil Permissions = full
			{Username: clientUser, Password: clPass, Permissions: clientPermissions()},
		},
	}

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("bus: new server: %w", err)
	}
	ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		ns.Shutdown()
		return nil, errors.New("bus: server not ready for connections")
	}

	b := &Bus{ns: ns, url: ns.ClientURL(), opPass: opPass, clPass: clPass}

	opConn, err := nats.Connect(b.url, nats.UserInfo(operatorUser, opPass), nats.Name("sextant-operator"))
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
		_, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  spec.Name,
			History: spec.History,
			Storage: jetstream.FileStorage,
		})
		if err != nil {
			return fmt.Errorf("bus: bootstrap bucket %s: %w", spec.Name, err)
		}
	}
	return nil
}

// clientPermissions builds the static client-tier guardrail (ADR-0012).
func clientPermissions() *natsserver.Permissions {
	return &natsserver.Permissions{
		Publish: &natsserver.SubjectPermission{
			Deny: []string{
				sx.ControlPrefix + ">",          // operator-only control
				"$KV." + sx.BucketSystem + ".>", // no system writes
				// No stream/bucket lifecycle (operator provisions buckets).
				"$JS.API.STREAM.CREATE.>",
				"$JS.API.STREAM.UPDATE.>",
				"$JS.API.STREAM.DELETE.>",
				"$JS.API.STREAM.PURGE.>",
			},
		},
		Subscribe: &natsserver.SubjectPermission{
			Deny: []string{
				"$KV." + sx.BucketSystem + ".>", // no system reads via consumer
			},
		},
	}
}

// Drain broadcasts the cooperative-drain control message (ADR-0010). Clients'
// SDKs wind down on receipt.
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

// ClientUser / ClientPassword are the client-tier credentials.
func (b *Bus) ClientUser() string     { return clientUser }
func (b *Bus) ClientPassword() string { return b.clPass }

// OperatorUser / OperatorPassword are the operator-tier credentials.
func (b *Bus) OperatorUser() string     { return operatorUser }
func (b *Bus) OperatorPassword() string { return b.opPass }

func randHex() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("bus: rand: %v", err)) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(buf[:])
}

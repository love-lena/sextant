package natsbackend

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/backend/conformance"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestConformance runs the shared backend conformance suite against the NATS
// module over an embedded in-memory JetStream server.
func TestConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Harness {
		js := embeddedJS(t)
		ctx := t.Context()
		if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
			Name:     "TESTLOG",
			Subjects: []string{"test.>"},
			Storage:  jetstream.MemoryStorage,
		}); err != nil {
			t.Fatalf("create stream: %v", err)
		}
		if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  "TESTKV",
			History: 10,
			Storage: jetstream.MemoryStorage,
		}); err != nil {
			t.Fatalf("create kv: %v", err)
		}
		return conformance.Harness{
			Backend:     New(js, "TESTLOG"),
			SubjectBase: "test",
			Bucket:      "TESTKV",
		}
	})
}

// embeddedJS starts a bare embedded NATS+JetStream server (no auth, in-memory)
// and returns a JetStream context bound to an in-process connection. The server
// and connection are torn down on test cleanup.
func embeddedJS(t *testing.T) jetstream.JetStream {
	t.Helper()
	opts := &natsserver.Options{
		ServerName: "backend-test",
		Host:       "127.0.0.1",
		Port:       -1,
		JetStream:  true,
		StoreDir:   t.TempDir(),
		NoSigs:     true,
	}
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		ns.Shutdown()
		t.Fatal("server not ready")
	}
	t.Cleanup(ns.Shutdown)
	nc, err := nats.Connect("", nats.InProcessServer(ns))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	return js
}

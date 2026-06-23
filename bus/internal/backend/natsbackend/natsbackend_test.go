package natsbackend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus/internal/backend"
	"github.com/love-lena/sextant/bus/internal/backend/conformance"
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

// TestSubscribeFromExpiredSequenceFailsLoud pins the FirstSeq bound of the
// StartFromSeq resume check (ADR-0027): after retention removes the head of the
// stream, resuming from a sequence below FirstSeq returns ErrSequenceGone
// rather than silently skipping the gap (JetStream's
// DeliverByStartSequencePolicy would otherwise start at FirstSeq without a
// word). A purge up to a sequence is the deterministic stand-in for MaxAge
// expiry — both advance FirstSeq the same way.
func TestSubscribeFromExpiredSequenceFailsLoud(t *testing.T) {
	js := embeddedJS(t)
	ctx := t.Context()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "EXPIRELOG",
		Subjects: []string{"expire.>"},
		Storage:  jetstream.MemoryStorage,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
	b := New(js, "EXPIRELOG")

	var seqs []uint64
	for range 5 {
		s, err := b.Append(ctx, "expire.t", []byte(`{"n":1}`))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		seqs = append(seqs, s)
	}
	// Expire the head: purge everything below the fourth entry, so FirstSeq
	// advances to seqs[3].
	stream, err := js.Stream(ctx, "EXPIRELOG")
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Purge(ctx, jetstream.WithPurgeSequence(seqs[3])); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Resuming below the new FirstSeq must fail loud — the messages between the
	// resume point and FirstSeq are gone and would otherwise be skipped silently.
	if _, err := b.Subscribe(ctx, "expire.t", backend.StartFromSeq, seqs[1]); !errors.Is(err, backend.ErrSequenceGone) {
		t.Fatalf("Subscribe below FirstSeq returned %v; want backend.ErrSequenceGone", err)
	}

	// Control: resuming exactly at FirstSeq is valid and delivers from there —
	// the bound is not over-broad.
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := b.Subscribe(subCtx, "expire.t", backend.StartFromSeq, seqs[3])
	if err != nil {
		t.Fatalf("Subscribe at FirstSeq: %v", err)
	}
	select {
	case e := <-ch:
		if e.Seq != seqs[3] {
			t.Fatalf("first resumed delivery seq = %d, want %d", e.Seq, seqs[3])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume at FirstSeq delivered nothing")
	}
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

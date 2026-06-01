package rpc_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestDispatcherRejectsStaleEpoch is the end-to-end WireEpoch acceptance
// check at the dispatch layer: a stale-proto_version RPC envelope gets a
// terminal wire_epoch_mismatch reply carrying the reinstall remedy, and
// never reaches a handler.
func TestDispatcherRejectsStaleEpoch(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s := newEpochTestServer(srv, nc, t)

	reply := nats.NewInbox()
	ch := make(chan *nats.Msg, 1)
	sub, err := nc.ChanSubscribe(reply, ch)
	if err != nil {
		t.Fatalf("subscribe reply: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, from, json.RawMessage(`{}`))
	env.ProtoVersion = "0.0.1-ancient"
	env.ReplyTo = &reply
	key := uuid.NewString()
	env.IdempotencyKey = &key
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := nc.Publish("sextant.rpc.list_agents", raw); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-ch:
		var respEnv sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &respEnv); err != nil {
			t.Fatalf("decode reply envelope: %v", err)
		}
		var resp sextantproto.RPCResponse
		if err := json.Unmarshal(respEnv.Payload, &resp); err != nil {
			t.Fatalf("decode reply payload: %v", err)
		}
		if resp.Error == nil {
			t.Fatal("expected wire_epoch_mismatch error, got success")
		}
		if resp.Error.Code != sextantproto.ErrCodeWireEpochMismatch {
			t.Fatalf("Code = %q, want %q", resp.Error.Code, sextantproto.ErrCodeWireEpochMismatch)
		}
		if !strings.Contains(resp.Error.Message, "make install") {
			t.Errorf("message lacks reinstall remedy: %q", resp.Error.Message)
		}
		if resp.Error.Details["remedy"] != "make install" {
			t.Errorf("details.remedy = %v, want \"make install\"", resp.Error.Details["remedy"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reply within 3s")
	}
	_ = s
}

// newEpochTestServer wires an rpc.Server with a trivial list_agents
// handler so the dispatcher has a real verb to (not) reach; it tears down
// via t.Cleanup. The srv arg is unused but keeps call sites self-
// documenting about which NATS instance the conn belongs to.
func newEpochTestServer(_ *natsboot.Server, nc *nats.Conn, t *testing.T) *rpc.Server {
	t.Helper()
	s, err := rpc.New(nc, rpc.Config{
		From: sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	if err := s.Register(rpc.VerbListAgents, func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{Terminal: true})
		return nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
	})
	time.Sleep(50 * time.Millisecond)
	return s
}

package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// fakeKV is an in-memory AgentKV. Tests build it with a fixed set of
// agent definitions; the handlers iterate Keys() like the real KV.
type fakeKV struct {
	entries map[string][]byte
	getErr  error
	listErr error
}

func (f *fakeKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return fakeEntry{key: key, value: v}, nil
}

func (f *fakeKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make(chan string, len(f.entries))
	for k := range f.entries {
		out <- k
	}
	close(out)
	return fakeLister{ch: out}, nil
}

type fakeLister struct{ ch chan string }

func (l fakeLister) Keys() <-chan string { return l.ch }
func (l fakeLister) Stop() error         { return nil }

type fakeEntry struct {
	key   string
	value []byte
}

func (e fakeEntry) Bucket() string                  { return handlers.AgentDefinitionsBucket }
func (e fakeEntry) Key() string                     { return e.key }
func (e fakeEntry) Value() []byte                   { return e.value }
func (e fakeEntry) Revision() uint64                { return 1 }
func (e fakeEntry) Created() time.Time              { return time.Time{} }
func (e fakeEntry) Delta() uint64                   { return 0 }
func (e fakeEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

// captureEmit collects RPCResponse calls and returns the captured value.
type captureEmit struct {
	resp sextantproto.RPCResponse
	hits int
}

func (c *captureEmit) emit() func(sextantproto.RPCResponse) {
	return func(r sextantproto.RPCResponse) {
		c.hits++
		c.resp = r
	}
}

// makeReq is a tiny envelope builder for handler tests.
func makeReq(t *testing.T, payload any) sextantproto.Envelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"}
	return sextantproto.NewEnvelope(sextantproto.KindRPCRequest, from, raw)
}

func TestListAgentsEmptyBucketReturnsEmptySlice(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	h := handlers.NewListAgents(kv)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.ListAgentsRequest{})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.hits != 1 {
		t.Fatalf("emit hits = %d, want 1", cap.hits)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %v, want nil", cap.resp.Error)
	}
	var resp sextantproto.ListAgentsResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Agents == nil {
		t.Fatal("Agents must be a non-nil (empty) slice")
	}
	if len(resp.Agents) != 0 {
		t.Fatalf("Agents = %v, want empty", resp.Agents)
	}
}

func TestListAgentsReturnsRegisteredAgents(t *testing.T) {
	id := uuid.New()
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      "alpha",
		Type:      "dev",
		Template:  "default",
		Lifecycle: sextantproto.LifecycleDefined,
		Version:   1,
		UpdatedAt: sextantproto.NowTimestamp(),
	}
	raw, _ := json.Marshal(def)
	kv := &fakeKV{entries: map[string][]byte{id.String(): raw}}
	h := handlers.NewListAgents(kv)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.ListAgentsRequest{})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %v, want nil", cap.resp.Error)
	}
	var resp sextantproto.ListAgentsResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(resp.Agents))
	}
	got := resp.Agents[0]
	if got.UUID != id || got.Name != "alpha" || got.Lifecycle != "defined" {
		t.Fatalf("AgentSummary = %+v", got)
	}
}

func TestGetAgentStatusUnknownReturnsAgentNotFound(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	h := handlers.NewGetAgentStatus(kv)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.GetAgentStatusRequest{AgentID: uuid.New()})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("Error must be set for unknown agent")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Code = %q, want %q", cap.resp.Error.Code, sextantproto.ErrCodeAgentNotFound)
	}
}

func TestGetAgentStatusKnownReturnsStatus(t *testing.T) {
	id := uuid.New()
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      "beta",
		Lifecycle: sextantproto.LifecycleRunning,
		Version:   3,
		UpdatedAt: sextantproto.NowTimestamp(),
	}
	raw, _ := json.Marshal(def)
	kv := &fakeKV{entries: map[string][]byte{id.String(): raw}}
	h := handlers.NewGetAgentStatus(kv)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.GetAgentStatusRequest{AgentID: id})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %v", cap.resp.Error)
	}
	var resp sextantproto.GetAgentStatusResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status.UUID != id || resp.Status.Name != "beta" || resp.Status.Lifecycle != "running" || resp.Status.Version != 3 {
		t.Fatalf("Status = %+v", resp.Status)
	}
}

func TestGetAgentStatusRejectsZeroUUID(t *testing.T) {
	kv := &fakeKV{entries: map[string][]byte{}}
	h := handlers.NewGetAgentStatus(kv)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.GetAgentStatusRequest{AgentID: uuid.Nil})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Fatalf("Error = %+v, want bad_request", cap.resp.Error)
	}
}

func TestReadFileStubReturnsNotImplemented(t *testing.T) {
	// NewReadFileStub is kept for callers that want a fast stub without
	// wiring the M12 container-exec backend. The real NewReadFile (with
	// FilesDeps) is exercised in files_test.go.
	h := handlers.NewReadFileStub()
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.ReadFileRequest{AgentID: uuid.New(), Path: "/etc/hosts"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("Error must be set")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeNotImplemented {
		t.Fatalf("Code = %q, want %q", cap.resp.Error.Code, sextantproto.ErrCodeNotImplemented)
	}
}

func TestListAgentsKVErrorSurfaces(t *testing.T) {
	kv := &fakeKV{listErr: errors.New("clickhouse exploded")}
	h := handlers.NewListAgents(kv)
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.ListAgentsRequest{})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeInternal {
		t.Fatalf("Error = %+v, want internal", cap.resp.Error)
	}
}

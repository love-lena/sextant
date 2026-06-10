package bus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/nats.go"
)

// startTestBus, connectClient, and friends live in bus_test.go.

func call(t *testing.T, nc *nats.Conn, id, op string, input any) wireapi.Response {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s input: %v", op, err)
	}
	msg, err := nc.Request(wireapi.CallSubject(id, op), data, 5*time.Second)
	if err != nil {
		t.Fatalf("call %s: %v", op, err)
	}
	var resp wireapi.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatalf("unmarshal %s response: %v", op, err)
	}
	return resp
}

func TestServePublishThenRead(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "client-a")
	subj := sx.TopicSubject("plan")

	resp := call(t, nc, id, wireapi.OpMessagePublish, wireapi.PublishInput{
		Subject: subj, Record: json.RawMessage(`{"hello":"world"}`),
	})
	if resp.Error != "" {
		t.Fatalf("publish: %s", resp.Error)
	}
	var pub wireapi.PublishOutput
	mustJSON(t, resp.Result, &pub)
	if pub.ID == "" || pub.Seq == 0 {
		t.Fatalf("bad publish output: %+v", pub)
	}

	rresp := call(t, nc, id, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subj, Since: 0, Limit: 10})
	if rresp.Error != "" {
		t.Fatalf("read: %s", rresp.Error)
	}
	var rd wireapi.ReadOutput
	mustJSON(t, rresp.Result, &rd)
	if len(rd.Messages) != 1 {
		t.Fatalf("read returned %d messages, want 1", len(rd.Messages))
	}
	f := rd.Messages[0]
	// The bus stamped the frame: author from the call's subject token, message kind.
	if f.Author != id {
		t.Errorf("stamped author = %q, want %q", f.Author, id)
	}
	if f.Kind != wire.KindMessage {
		t.Errorf("kind = %q, want message", f.Kind)
	}
	if f.ID != pub.ID {
		t.Errorf("read frame id %q != published id %q", f.ID, pub.ID)
	}
	if string(f.Record) != `{"hello":"world"}` {
		t.Errorf("record = %s", f.Record)
	}
}

func TestServeReadCursorResumes(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "reader")
	subj := sx.TopicSubject("log")
	for i := 0; i < 3; i++ {
		resp := call(t, nc, id, wireapi.OpMessagePublish, wireapi.PublishInput{Subject: subj, Record: json.RawMessage(`{"n":1}`)})
		if resp.Error != "" {
			t.Fatalf("publish: %s", resp.Error)
		}
	}
	var rd wireapi.ReadOutput
	mustJSON(t, call(t, nc, id, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subj, Since: 0, Limit: 10}).Result, &rd)
	if len(rd.Messages) != 3 {
		t.Fatalf("first read got %d, want 3", len(rd.Messages))
	}
	var rd2 wireapi.ReadOutput
	mustJSON(t, call(t, nc, id, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subj, Since: rd.NextCursor, Limit: 10}).Result, &rd2)
	if len(rd2.Messages) != 0 {
		t.Fatalf("resume read got %d, want 0", len(rd2.Messages))
	}
}

func TestServeArtifactLifecycle(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "author-1")

	// create
	var w wireapi.ArtifactWriteOutput
	resp := call(t, nc, id, wireapi.OpArtifactCreate, wireapi.ArtifactCreateInput{Name: "the-plan", Record: json.RawMessage(`{"title":"v1"}`)})
	if resp.Error != "" {
		t.Fatalf("create: %s", resp.Error)
	}
	mustJSON(t, resp.Result, &w)
	if w.Revision != 1 {
		t.Fatalf("create revision = %d, want 1", w.Revision)
	}

	// get
	var g wireapi.ArtifactGetOutput
	mustJSON(t, call(t, nc, id, wireapi.OpArtifactGet, wireapi.ArtifactGetInput{Name: "the-plan"}).Result, &g)
	if string(g.Record) != `{"title":"v1"}` || g.Revision != 1 {
		t.Fatalf("get = %+v", g)
	}
	if g.CreatedAt == "" || g.UpdatedAt == "" {
		t.Errorf("bus did not stamp timestamps: %+v", g)
	}

	// update at rev 1 -> rev 2
	var w2 wireapi.ArtifactWriteOutput
	uresp := call(t, nc, id, wireapi.OpArtifactUpdate, wireapi.ArtifactUpdateInput{Name: "the-plan", Record: json.RawMessage(`{"title":"v2"}`), ExpectedRev: 1})
	if uresp.Error != "" {
		t.Fatalf("update: %s", uresp.Error)
	}
	mustJSON(t, uresp.Result, &w2)
	if w2.Revision <= 1 {
		t.Fatalf("update revision = %d, want > 1", w2.Revision)
	}

	// stale update at rev 1 -> rejected (compare-and-set)
	stale := call(t, nc, id, wireapi.OpArtifactUpdate, wireapi.ArtifactUpdateInput{Name: "the-plan", Record: json.RawMessage(`{"title":"v3"}`), ExpectedRev: 1})
	if stale.Error == "" {
		t.Error("stale update should have been rejected (revision mismatch)")
	}

	// delete -> get errors
	if del := call(t, nc, id, wireapi.OpArtifactDelete, wireapi.ArtifactDeleteInput{Name: "the-plan"}); del.Error != "" {
		t.Fatalf("delete: %s", del.Error)
	}
	if g2 := call(t, nc, id, wireapi.OpArtifactGet, wireapi.ArtifactGetInput{Name: "the-plan"}); g2.Error == "" {
		t.Error("get after delete should error")
	}
}

// TestServeArtifactListSkipsUndecodable: a single undecodable frame in the
// artifacts bucket must not fail the listing for everyone — the decodable rest
// is still returned — and the drop must not be silent: one line through
// Config.Logf names the artifact and the decode error. The mangled value is
// seeded directly via the backend (the bus encodes every frame a client
// writes, so only store corruption can produce one).
func TestServeArtifactListSkipsUndecodable(t *testing.T) {
	rec := &logRecorder{}
	b, err := Start(t.Context(), Config{StoreDir: t.TempDir(), Logf: rec.logf})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	nc, id := connectClient(t, b, "list-survivor")

	// A well-formed artifact through the API…
	if resp := call(t, nc, id, wireapi.OpArtifactCreate, wireapi.ArtifactCreateInput{
		Name: "good", Record: json.RawMessage(`{"ok":true}`),
	}); resp.Error != "" {
		t.Fatalf("create good: %s", resp.Error)
	}
	// …and an undecodable frame seeded under it in the same bucket.
	if _, err := b.backend.Put(t.Context(), sx.BucketArtifacts, "mangled", []byte("not a wire frame")); err != nil {
		t.Fatalf("seed mangled artifact: %v", err)
	}

	resp := call(t, nc, id, wireapi.OpArtifactList, struct{}{})
	if resp.Error != "" {
		t.Fatalf("artifact.list should skip the undecodable frame, not error: %s", resp.Error)
	}
	var out wireapi.ArtifactListOutput
	mustJSON(t, resp.Result, &out)
	if len(out.Artifacts) != 1 || out.Artifacts[0].Name != "good" {
		t.Fatalf("listing should return exactly the decodable artifact, got %+v", out.Artifacts)
	}

	var sawDrop bool
	for _, line := range rec.all() {
		if strings.Contains(line, "artifact.list") && strings.Contains(line, `"mangled"`) {
			sawDrop = true
		}
	}
	if !sawDrop {
		t.Errorf("dropping the undecodable artifact logged nothing through Config.Logf: %q", rec.all())
	}
}

func TestServeClientsList(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "lister")
	// Seed a durable identity record directly via the backend (no live connection),
	// to exercise the listing + presence join: it must appear, and be offline (no
	// connection authenticates as its subject).
	rec, _ := json.Marshal(wireapi.ClientEntry{
		ID: "ghost", Kind: "harness", Epoch: wire.Epoch,
		Subject:  "UGHOSTSUBJECTKEY",
		IssuedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if _, err := b.backend.Put(t.Context(), sx.BucketClients, "ghost", rec); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	var out wireapi.ClientsListOutput
	mustJSON(t, call(t, nc, id, wireapi.OpClientsList, struct{}{}).Result, &out)
	var ghost *wireapi.ClientEntry
	for i := range out.Clients {
		if out.Clients[i].ID == "ghost" {
			ghost = &out.Clients[i]
		}
	}
	if ghost == nil || ghost.Kind != "harness" {
		t.Fatalf("clients.list did not return the seeded entry: %+v", out.Clients)
	}
	if ghost.Presence != wireapi.PresenceOffline {
		t.Errorf("seeded ghost (no connection) should be offline, got %q", ghost.Presence)
	}
}

func mustJSON(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal result: %v (data=%s)", err, data)
	}
}

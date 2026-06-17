package bus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/backend"
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

// TestPresenceDualSource pins the leaf-correct presence rule (TASK-126):
// online = Connz-online OR last_seen within the freshness window. The four cases:
//
//   - Connz-online + no beat → online (back-compat: a directly connected client
//     with a pre-TASK-126 credential is unaffected).
//   - not in Connz + fresh beat → online (the leaf case: a client behind a leaf
//     link that Connz cannot see is held online by its fresh beats).
//   - not in Connz + stale beat → offline (the beat aged out of the window).
//   - not in Connz + no beat → offline (the baseline).
//
// Seeded records carry a subject that is NOT in the connection table, so Connz
// reports them offline — isolating the last_seen contribution. The reader is a
// real connected client; its own (online-via-Connz) entry is ignored here.
func TestPresenceDualSource(t *testing.T) {
	b := startTestBus(t)
	b.SetFreshnessWindow(2 * time.Second) // tight window so "stale" is easy to construct
	nc, readerID := connectClient(t, b, "presence-reader")

	now := time.Now().UTC()
	seed := func(id, lastSeen string) {
		t.Helper()
		rec, _ := json.Marshal(wireapi.ClientEntry{
			ID: id, Kind: "harness", Epoch: wire.Epoch,
			Subject:  "USUBJECT-" + id, // never in the connection table → Connz-offline
			IssuedAt: now.Format(time.RFC3339),
			LastSeen: lastSeen,
		})
		if err := b.SeedClientRecord(t.Context(), id, rec); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("fresh-beat", now.Add(-500*time.Millisecond).Format(time.RFC3339))
	seed("stale-beat", now.Add(-1*time.Hour).Format(time.RFC3339))
	seed("no-beat", "")

	var out wireapi.ClientsListOutput
	mustJSON(t, call(t, nc, readerID, wireapi.OpClientsList, struct{}{}).Result, &out)
	got := map[string]string{}
	for _, e := range out.Clients {
		got[e.ID] = e.Presence
	}

	if got[readerID] != wireapi.PresenceOnline {
		t.Errorf("Connz-online reader (no beat) should be online, got %q", got[readerID])
	}
	if got["fresh-beat"] != wireapi.PresenceOnline {
		t.Errorf("not-in-Connz + fresh beat should be online (leaf case), got %q", got["fresh-beat"])
	}
	if got["stale-beat"] != wireapi.PresenceOffline {
		t.Errorf("not-in-Connz + stale beat should be offline, got %q", got["stale-beat"])
	}
	if got["no-beat"] != wireapi.PresenceOffline {
		t.Errorf("not-in-Connz + no beat should be offline, got %q", got["no-beat"])
	}
}

// TestHeartbeatStampsLastSeenAndEchoes pins the round-trip heartbeat (TASK-126):
// clients.heartbeat (a) stamps a bus-clock last_seen on the caller's registry
// record, (b) core-publishes a HeartbeatEcho carrying the same Seq to the
// dedicated sx.hb.<id> subject, and (c) returns the bus-stamped ServerTime.
func TestHeartbeatStampsLastSeenAndEchoes(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "beater")

	// Subscribe the echo subject before beating, so the transient core publish
	// is not missed.
	echoSub, err := nc.SubscribeSync(wireapi.HeartbeatSubject(id))
	if err != nil {
		t.Fatalf("subscribe echo: %v", err)
	}
	_ = nc.Flush()

	resp := call(t, nc, id, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: 7})
	if resp.Error != "" {
		t.Fatalf("heartbeat: %s", resp.Error)
	}
	var out wireapi.HeartbeatOutput
	mustJSON(t, resp.Result, &out)
	if out.ServerTime == "" {
		t.Error("heartbeat did not stamp a server time")
	}
	if _, perr := time.Parse(time.RFC3339, out.ServerTime); perr != nil {
		t.Errorf("ServerTime %q is not RFC3339: %v", out.ServerTime, perr)
	}

	// (a) last_seen is recorded on the registry record, matching ServerTime.
	val, _, err := b.backend.Get(t.Context(), sx.BucketClients, id)
	if err != nil {
		t.Fatalf("read registry record: %v", err)
	}
	var e wireapi.ClientEntry
	mustJSON(t, val, &e)
	if e.LastSeen == "" {
		t.Error("heartbeat did not stamp last_seen on the registry record")
	}
	if e.LastSeen != out.ServerTime {
		t.Errorf("last_seen %q != returned ServerTime %q", e.LastSeen, out.ServerTime)
	}
	// The other identity fields must survive the read-modify-write.
	if e.DisplayName != "beater" {
		t.Errorf("heartbeat clobbered display_name: %q", e.DisplayName)
	}

	// (b) the echo lands on sx.hb.<id> with the same Seq.
	msg, err := echoSub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("no heartbeat echo on %s: %v", wireapi.HeartbeatSubject(id), err)
	}
	var echo wireapi.HeartbeatEcho
	mustJSON(t, msg.Data, &echo)
	if echo.Seq != 7 {
		t.Errorf("echo Seq = %d, want 7", echo.Seq)
	}
}

// TestHeartbeatUnknownIdentityRejected: a heartbeat from a caller with no durable
// record (never issued, or retired) is rejected, like hello — the record is the
// thing it modifies, so there is nothing to stamp.
func TestHeartbeatUnknownIdentityRejected(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "beater-gone")
	if err := b.DeleteClientRecord(t.Context(), id); err != nil {
		t.Fatalf("delete record: %v", err)
	}
	if resp := call(t, nc, id, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: 1}); resp.Error == "" {
		t.Error("heartbeat for an unknown/retired identity must be rejected")
	}
}

// TestHeartbeatAfterRetireDoesNotResurrect pins retire as a hard decommissioning
// boundary: a beat must NEVER recreate a retired record. Here the record is gone
// before the beat, so the beat fails on the not-found gate and the record stays
// gone (the simple, deterministic case).
func TestHeartbeatAfterRetireDoesNotResurrect(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "beater-retired")
	if err := b.DeleteClientRecord(t.Context(), id); err != nil { // stands in for retire
		t.Fatalf("retire (delete record): %v", err)
	}
	if resp := call(t, nc, id, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: 1}); resp.Error == "" {
		t.Fatal("heartbeat after retire must fail, not resurrect the record")
	}
	if _, _, err := b.backend.Get(t.Context(), sx.BucketClients, id); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("retired record must stay gone after a beat, Get err = %v", err)
	}
}

// TestHeartbeatRetireRaceDoesNotResurrect forces the exact interleave Codex
// flagged: the beat reads the record (and its revision) BEFORE the retire-delete,
// but its write runs AFTER. An unconditional Put would resurrect the record; a
// revision-aware write (CompareAndSet at the read revision) fails with ErrNotFound
// instead, which the handler maps to the same "not registered/retired" rejection —
// never a recreate. A hook fires the delete inside the post-read window.
func TestHeartbeatRetireRaceDoesNotResurrect(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "beater-racer")

	var fired bool
	b.SetHeartbeatAfterReadHook(func() {
		if fired {
			return // the retry path re-reads; only delete in the first read window
		}
		fired = true
		if err := b.DeleteClientRecord(context.Background(), id); err != nil {
			t.Errorf("hook delete: %v", err)
		}
	})

	if resp := call(t, nc, id, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: 1}); resp.Error == "" {
		t.Fatal("a beat whose write loses to a concurrent retire must fail, not resurrect")
	}
	if _, _, err := b.backend.Get(t.Context(), sx.BucketClients, id); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("the retired record must stay gone (beat must not recreate it), Get err = %v", err)
	}
}

func mustJSON(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal result: %v (data=%s)", err, data)
	}
}

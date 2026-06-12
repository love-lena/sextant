package dashapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

// get issues an authorized GET against srv and returns the recorder.
func get(srv http.Handler, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestSelfReportsIdentityAndPrincipal(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME", display: "orion", principal: "01PRIN"}, "tok")
	rec := get(srv, "/api/self")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Principal   string `json:"principal"`
	}
	mustDecode(t, rec, &got)
	if got.ID != "01ME" || got.DisplayName != "orion" || got.Principal != "01PRIN" {
		t.Fatalf("self = %+v", got)
	}
}

// TestClientsParity is the contract that makes the API a faithful mirror of the
// CLI: GET /api/clients returns exactly the directory ListClients yields, so the
// demo can cross-check it against `sextant clients list`.
func TestClientsParity(t *testing.T) {
	clients := []sextant.ClientInfo{
		{ID: "01A", DisplayName: "alice", Kind: "human", Epoch: 1, Online: true, IssuedAt: time.Unix(1, 0).UTC()},
		{ID: "01B", DisplayName: "bob", Kind: "agent", Epoch: 1, Online: false, IssuedAt: time.Unix(2, 0).UTC()},
	}
	srv := newServer(&fakeBus{clients: clients}, "tok")
	rec := get(srv, "/api/clients")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got []sextant.ClientInfo
	mustDecode(t, rec, &got)
	if !reflect.DeepEqual(got, clients) {
		t.Fatalf("clients = %+v, want %+v", got, clients)
	}
}

func TestArtifactsParity(t *testing.T) {
	arts := []sextant.ArtifactInfo{
		{Name: "plan", Revision: 3, Created: time.Unix(1, 0).UTC(), Updated: time.Unix(9, 0).UTC()},
	}
	srv := newServer(&fakeBus{artifacts: arts}, "tok")
	rec := get(srv, "/api/artifacts")
	var got []sextant.ArtifactInfo
	mustDecode(t, rec, &got)
	if !reflect.DeepEqual(got, arts) {
		t.Fatalf("artifacts = %+v, want %+v", got, arts)
	}
}

func TestArtifactGetByName(t *testing.T) {
	art := sextant.Artifact{Name: "plan", Record: wire.Lexicon(`{"$type":"document","title":"X"}`), Revision: 2, Created: time.Unix(1, 0).UTC()}
	srv := newServer(&fakeBus{artifact: map[string]sextant.Artifact{"plan": art}}, "tok")

	rec := get(srv, "/api/artifacts/plan")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got sextant.Artifact
	mustDecode(t, rec, &got)
	// The record round-trips as semantically-equal JSON; writeJSON pretty-prints
	// (as the CLI's `artifact get --json` does), so compare compacted bytes.
	if got.Name != "plan" || got.Revision != 2 || compact(t, got.Record) != `{"$type":"document","title":"X"}` {
		t.Fatalf("artifact = %+v (record %s)", got, got.Record)
	}
}

func TestArtifactGetMissingIs404(t *testing.T) {
	srv := newServer(&fakeBus{artifact: map[string]sextant.Artifact{}}, "tok")
	rec := get(srv, "/api/artifacts/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// TestMessagesPassesQueryThrough checks the read contract: subject/since/limit
// reach FetchMessages and the response carries the frames plus the next cursor.
func TestMessagesPassesQueryThrough(t *testing.T) {
	frames := []wire.Frame{
		{ID: "01F1", Author: "01A", Kind: wire.KindMessage, Epoch: 1, Record: wire.Lexicon(`{"$type":"chat.message","text":"hi"}`)},
	}
	bus := &fakeBus{frames: frames, nextCur: 42}
	srv := newServer(bus, "tok")

	rec := get(srv, "/api/messages?subject=msg.topic.plan&since=7&limit=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Messages   []wire.Frame `json:"messages"`
		NextCursor uint64       `json:"next_cursor"`
	}
	mustDecode(t, rec, &got)
	if got.NextCursor != 42 || len(got.Messages) != 1 || got.Messages[0].ID != "01F1" {
		t.Fatalf("messages response = %+v", got)
	}
	if bus.lastFetchSubj != "msg.topic.plan" || bus.lastFetchSince != 7 || bus.lastFetchLimit != 5 {
		t.Fatalf("fetch args = (%q, %d, %d), want (msg.topic.plan, 7, 5)", bus.lastFetchSubj, bus.lastFetchSince, bus.lastFetchLimit)
	}
}

func TestMessagesRequiresSubject(t *testing.T) {
	srv := newServer(&fakeBus{}, "tok")
	rec := get(srv, "/api/messages")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing subject (%s)", rec.Code, rec.Body.String())
	}
}

// compact strips insignificant whitespace so a record can be compared by content
// regardless of the response's pretty-printing.
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return buf.String()
}

func mustDecode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

package dashapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// TestSubjectsRegistryListsSeen: Watch records every subject seen on msg.>, and
// /api/subjects returns them sorted with per-subject frame counts.
func TestSubjectsRegistryListsSeen(t *testing.T) {
	bus := &fakeBus{id: "01ME"}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok"})
	if err := srv.Watch(context.Background()); err != nil {
		t.Fatal(err)
	}
	bus.push(sextant.Message{Subject: "msg.topic.plan", Frame: wire.Frame{ID: "1"}})
	bus.push(sextant.Message{Subject: "msg.topic.plan", Frame: wire.Frame{ID: "2"}})
	bus.push(sextant.Message{Subject: "msg.client.01ME", Frame: wire.Frame{ID: "3"}})

	req := httptest.NewRequest(http.MethodGet, "/api/subjects", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out []subjectStatJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("subjects = %d, want 2 (%+v)", len(out), out)
	}
	// sorted: msg.client.* before msg.topic.*
	if out[0].Subject != "msg.client.01ME" || out[0].Count != 1 {
		t.Fatalf("out[0] = %+v, want msg.client.01ME count 1", out[0])
	}
	if out[1].Subject != "msg.topic.plan" || out[1].Count != 2 {
		t.Fatalf("out[1] = %+v, want msg.topic.plan count 2", out[1])
	}
}

// TestSubjectsRequiresToken: the registry is gated like every /api call.
func TestSubjectsRequiresToken(t *testing.T) {
	srv := dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01ME"}, Token: "tok"})
	req := httptest.NewRequest(http.MethodGet, "/api/subjects", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

type subjectStatJSON struct {
	Subject string `json:"subject"`
	Count   uint64 `json:"count"`
}

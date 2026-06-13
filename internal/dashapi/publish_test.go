package dashapi_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// post issues an authorized POST with a JSON body.
func post(srv http.Handler, url, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestPublishForwardsToBus(t *testing.T) {
	bus := &fakeBus{}
	srv := newServer(bus, "tok")

	rec := post(srv, "/api/publish", `{"subject":"msg.topic.plan","record":{"$type":"chat.message","text":"hi"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if bus.publishedCount() != 1 {
		t.Fatalf("published %d, want 1", bus.publishedCount())
	}
	bus.mu.Lock()
	got := bus.published[0]
	bus.mu.Unlock()
	if got.subject != "msg.topic.plan" {
		t.Fatalf("subject = %q, want msg.topic.plan", got.subject)
	}
	if compact(t, got.record) != `{"$type":"chat.message","text":"hi"}` {
		t.Fatalf("record = %s", got.record)
	}
}

func TestPublishRequiresSubjectAndRecord(t *testing.T) {
	srv := newServer(&fakeBus{}, "tok")
	cases := []struct {
		name string
		body string
	}{
		{"missing subject", `{"record":{"$type":"chat.message"}}`},
		{"empty subject", `{"subject":"","record":{"$type":"chat.message"}}`},
		{"missing record", `{"subject":"msg.topic.x"}`},
		{"malformed json", `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(srv, "/api/publish", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPublishBusErrorIs502(t *testing.T) {
	srv := newServer(&fakeBus{publishErr: errors.New("bus rejected")}, "tok")
	rec := post(srv, "/api/publish", `{"subject":"msg.topic.x","record":{"$type":"chat.message"}}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
}

// TestPublishRequiresToken confirms the command path is gated too, not just reads.
func TestPublishRequiresToken(t *testing.T) {
	srv := newServer(&fakeBus{}, "tok")
	req := httptest.NewRequest(http.MethodPost, "/api/publish", strings.NewReader(`{"subject":"msg.topic.x","record":{}}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

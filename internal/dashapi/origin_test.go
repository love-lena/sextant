package dashapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/internal/dashapi"
)

// withOrigin issues an authorized request carrying an Origin header.
func withOrigin(srv http.Handler, method, url, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestLocalhostOriginAllowed: a browser page served on localhost (any port) may
// call the API — this is the same-origin debug surface and a local dev server.
func TestLocalhostOriginAllowed(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	for _, origin := range []string{"http://127.0.0.1:8765", "http://localhost:5173", "http://[::1]:9000"} {
		rec := withOrigin(srv, http.MethodGet, "/api/self", origin)
		if rec.Code != http.StatusOK {
			t.Fatalf("origin %q: status = %d, want 200", origin, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("origin %q: ACAO = %q, want echo", origin, got)
		}
	}
}

// TestForeignOriginRejected: a page on some other site cannot drive the local
// API even if it somehow had the token — the origin guard is defense in depth.
func TestForeignOriginRejected(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	rec := withOrigin(srv, http.MethodGet, "/api/self", "https://evil.example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestConfiguredOriginAllowed: an explicitly-allowed extra origin (a D2 dev
// server on a non-localhost host) is permitted.
func TestConfiguredOriginAllowed(t *testing.T) {
	srv := dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01ME"}, Token: "tok", AllowedOrigins: []string{"https://dash.dev.internal"}})
	rec := withOrigin(srv, http.MethodGet, "/api/self", "https://dash.dev.internal")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// TestPreflightAllowedOrigin: a CORS preflight from an allowed origin gets the
// allow headers and 204, so the real request can follow.
func TestPreflightAllowedOrigin(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	req := httptest.NewRequest(http.MethodOptions, "/api/publish", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("preflight missing Access-Control-Allow-Methods")
	}
}

// TestNoOriginAllowed: a non-browser caller (curl, no Origin header) is not
// origin-gated — only token-gated.
func TestNoOriginAllowed(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	rec := get(srv, "/api/self")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

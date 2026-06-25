package dashapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/clients/sextant-dash/dashapi"
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

// originServer builds a Server with a ws URL so the mint endpoint succeeds, plus
// any extra allowed origins.
func originServer(extra ...string) *dashapi.Server {
	return dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01ME"}, Token: "tok", WSURL: "ws://127.0.0.1:7423", AllowedOrigins: extra})
}

// TestLocalhostOriginAllowed: a browser page served on localhost (any port) may
// call the API — this is the same-origin SPA and a local dev server.
func TestLocalhostOriginAllowed(t *testing.T) {
	srv := originServer()
	for _, origin := range []string{"http://127.0.0.1:8765", "http://localhost:5173", "http://[::1]:9000"} {
		rec := withOrigin(srv, http.MethodPost, "/api/session", origin)
		if rec.Code != http.StatusOK {
			t.Fatalf("origin %q: status = %d, want 200 (%s)", origin, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("origin %q: ACAO = %q, want echo", origin, got)
		}
	}
}

// TestForeignOriginRejected: a page on some other site cannot drive the local API
// even if it somehow had the token — the origin guard is defense in depth.
func TestForeignOriginRejected(t *testing.T) {
	srv := originServer()
	rec := withOrigin(srv, http.MethodPost, "/api/session", "https://evil.example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestConfiguredOriginAllowed: an explicitly-allowed extra origin (a dev server on
// a non-localhost host) is permitted.
func TestConfiguredOriginAllowed(t *testing.T) {
	srv := originServer("https://dash.dev.internal")
	rec := withOrigin(srv, http.MethodPost, "/api/session", "https://dash.dev.internal")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// TestPreflightAllowedOrigin: a CORS preflight from an allowed origin gets the
// allow headers and 204, so the real request can follow.
func TestPreflightAllowedOrigin(t *testing.T) {
	srv := originServer()
	req := httptest.NewRequest(http.MethodOptions, "/api/session", nil)
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
	srv := originServer()
	req := httptest.NewRequest(http.MethodPost, "/api/session", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

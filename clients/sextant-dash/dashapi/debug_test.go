package dashapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/sextant-dash/dashapi"
)

// TestRootServesDesignedApp: GET / serves the embedded designed UI (D2, TASK-71)
// — its index.html, which boots the React app into #root and pulls app.jsx. No
// token (static assets carry no secrets; the token in the URL gates the API).
func TestRootServesDesignedApp(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, marker := range []string{`id="root"`, "app.js", "styles.css"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("designed app missing %q", marker)
		}
	}
}

// TestDebugSurfaceAtDebugPath: GET /debug still serves the built-in zero-design
// page (no token — it carries no data). The page itself is a legacy harness for
// the old local-API surface (the /api/* relay it called is gone, ADR-0044 — the
// designed SPA at / is now the live surface, over the bus WebSocket); the route is
// kept so the static-host contract is unchanged. This guards only that /debug is
// served as HTML, not its (legacy) contents.
func TestDebugSurfaceAtDebugPath(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	req := httptest.NewRequest(http.MethodGet, "/debug", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
}

// TestRootUnknownPathIs404: the app file server 404s an unknown asset path
// rather than echoing index.html (no SPA catch-all — the UI is state-driven, not
// URL-routed).
func TestRootUnknownPathIs404(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	req := httptest.NewRequest(http.MethodGet, "/nope.css", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestUIDirOverridesBuiltIn: --ui <dir> serves a custom frontend (the dev
// override) instead of the embedded designed UI.
func TestUIDirOverridesBuiltIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>custom UI</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01ME"}, Token: "tok", UIDir: dir})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "custom UI") {
		t.Fatalf("status = %d body = %q, want custom UI", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("ui dir Cache-Control = %q, want no-store (hot-reload)", cc)
	}
}

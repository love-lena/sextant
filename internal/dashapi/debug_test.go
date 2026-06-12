package dashapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/internal/dashapi"
)

// TestRootServesDebugSurface: GET / returns the built-in zero-design page (no
// token needed — it carries no data; the token in its URL gates the API it
// calls). The page must wire the live stream and read its token, or it is not a
// working verification harness.
func TestRootServesDebugSurface(t *testing.T) {
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
	for _, marker := range []string{"EventSource", "/api/stream", "/api/clients", "/api/publish", "token"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("debug page missing %q", marker)
		}
	}
}

// TestRootUnknownPathIs404: with the built-in page, only / serves; other paths
// 404 rather than echoing the page.
func TestRootUnknownPathIs404(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "tok")
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestUIDirOverridesBuiltIn: --ui <dir> serves a custom frontend (the D2 hook)
// instead of the built-in page.
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
}

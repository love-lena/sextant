package dashapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/internal/dashapi"
)

// newServer builds a Server over a fakeBus with a fixed token, the shape every
// handler test starts from.
func newServer(bus dashapi.Bus, token string) *dashapi.Server {
	return dashapi.New(dashapi.Config{Bus: bus, Token: token})
}

// TestAPIRequiresToken is the access-control guard: every /api request must carry
// the per-launch token, as a Bearer header or a ?token= query param, or it is
// rejected 401. The token is the capability that gates the local API.
func TestAPIRequiresToken(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME", display: "me", principal: "01PRIN"}, "secret-token")

	cases := []struct {
		name  string
		auth  string
		query string
		want  int
	}{
		{"no token", "", "", http.StatusUnauthorized},
		{"wrong bearer", "Bearer nope", "", http.StatusUnauthorized},
		{"wrong query", "", "nope", http.StatusUnauthorized},
		{"good bearer", "Bearer secret-token", "", http.StatusOK},
		{"good query", "", "secret-token", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/self"
			if tc.query != "" {
				url += "?token=" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestEmptyTokenNeverMatches guards against a misconfiguration: a server built
// with an empty token must not accept a tokenless request (which would expose
// the bus to any localhost process). An empty configured token rejects all /api.
func TestEmptyTokenNeverMatches(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "")
	req := httptest.NewRequest(http.MethodGet, "/api/self", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for empty-token server", rec.Code)
	}
}

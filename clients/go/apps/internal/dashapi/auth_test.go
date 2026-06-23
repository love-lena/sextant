package dashapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
)

// newServer builds a Server over a fakeBus with a fixed token + a ws URL, the
// shape the mint-endpoint tests start from. The ws URL is what handleSession hands
// the page (ADR-0044).
func newServer(bus dashapi.Bus, token string) *dashapi.Server {
	return dashapi.New(dashapi.Config{Bus: bus, Token: token, WSURL: "ws://127.0.0.1:7423"})
}

// TestAPIRequiresToken is the access-control guard: a non-loopback /api request
// must carry the per-launch token, as a Bearer header or a ?token= query param, or
// it is rejected 401. The token is the capability that gates the mint endpoint.
// (httptest's default RemoteAddr 192.0.2.1 is non-loopback, so the token gate
// applies — loopback's tokenless path is covered separately.)
func TestAPIRequiresToken(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "secret-token")

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
			url := "/api/session"
			if tc.query != "" {
				url += "?token=" + tc.query
			}
			req := httptest.NewRequest(http.MethodPost, url, nil)
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

// TestLoopbackBypassesToken: a request from a loopback peer (127.0.0.1 / ::1) is
// authorized WITHOUT a token (TASK-115, ADR-0032 loopback exception) — the dash's
// listener is loopback-bound, and loopback is host-bound + implicitly trusted
// (standard localhost practice). Non-loopback still requires the token.
func TestLoopbackBypassesToken(t *testing.T) {
	srv := newServer(&fakeBus{id: "01ME"}, "secret-token")

	for _, addr := range []string{"127.0.0.1:5000", "[::1]:5000"} {
		req := httptest.NewRequest(http.MethodPost, "/api/session", nil)
		req.RemoteAddr = addr // a loopback peer
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("loopback %s without a token: status = %d, want 200 (body %q)", addr, rec.Code, rec.Body.String())
		}
	}

	// Non-loopback (httptest's default RemoteAddr is 192.0.2.1) still needs the token.
	req := httptest.NewRequest(http.MethodPost, "/api/session", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-loopback without a token: status = %d, want 401", rec.Code)
	}
}

// TestEmptyTokenNeverMatches guards against a misconfiguration: a server built
// with an empty token must not accept a tokenless non-loopback request (which
// would let any remote peer mint a browser credential). An empty configured token
// rejects all non-loopback /api.
func TestEmptyTokenNeverMatches(t *testing.T) {
	srv := dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01ME"}, Token: "", WSURL: "ws://127.0.0.1:7423"})
	req := httptest.NewRequest(http.MethodPost, "/api/session", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for empty-token server", rec.Code)
	}
}
